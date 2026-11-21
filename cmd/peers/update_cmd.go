package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wangzw/peers/internal/broker/service"
	"github.com/wangzw/peers/internal/broker/update"
)

// updateLocalTakesEffectHint follows a local replacement. The command does not
// restart anything, because the service may have been stopped on purpose.
const updateLocalTakesEffectHint = "The new binary takes effect the next time the Broker starts."

// updateSessionHint follows a Broker-applied update. It is the wording in
// design-broker's delegated update section; keep it verbatim.
const updateSessionHint = "Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically."

// updateRestartWait bounds the wait for a restarted Broker to report the new
// version. It covers the Broker's exit delay, the service manager's restart
// interval (systemd RestartSec is 5 seconds; launchd's default
// ThrottleInterval is 10 seconds) and the Broker's startup and readiness. The
// design sets the bound to 60 seconds. It is a variable so a test can shorten
// the wait without sleeping for the production bound.
var updateRestartWait = 60 * time.Second

// updateStatusPollDelay separates UpdateStatus polls while the Broker
// restarts. It is a variable for the same reason as updateRestartWait.
var updateStatusPollDelay = 250 * time.Millisecond

// Test seams for the parts of the update command that reach the operating
// system. The command's GitHub requests are reached through deps so an
// ordinary test double covers them as well.
var (
	updateExecutable   = os.Executable
	updateEvalSymlinks = filepath.EvalSymlinks
	updateNewClient    = func() *http.Client { return &http.Client{Transport: http.DefaultTransport} }
	updateGOOS         = runtime.GOOS
	updateGOARCH       = runtime.GOARCH
)

// newUpdateCommand builds "peers update [--yes]".
func newUpdateCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Install the newest release of peers",
		GroupID: groupInstall,
		Args:    noArgs,
	}
	yes := addYesFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return a.updateCmd(cmd.Context(), *yes)
	}
	return cmd
}

// noArgs rejects positional arguments as a usage error. The framework turns the
// error's exit code into 2 and prints the command path and help hint.
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf("unexpected argument %q", args[0])
	}
	return nil
}

// updateCmd implements "peers update [--yes]". It asks the selected
// Broker to install the newest release and restart itself. A user-level Broker
// that declines (it cannot replace and restart itself) leaves the command to
// replace that account's binary in place.
func (a *app) updateCmd(ctx context.Context, yes bool) error {
	current := strings.TrimPrefix(a.version, "v")
	if !update.IsReleaseVersion(current) {
		return fmt.Errorf("version %q is not a release build; only release builds can update themselves", a.version)
	}

	if !yes {
		confirmed, err := a.confirm(fmt.Sprintf("peers %s is installed. Update to the latest release? [y/N] ", current), false)
		if err != nil {
			return err
		}
		if !confirmed {
			_, _ = fmt.Fprintln(a.stdout, "Update canceled; nothing was changed.")
			return nil
		}
	}

	result, err := a.deps.applyUpdateViaBroker(ctx)
	if err != nil {
		if errors.Is(err, errUserLevelUpdateUnsupported) || errors.Is(err, errNoRunningBroker) {
			return a.updateLocal(ctx, current)
		}
		return err
	}
	return a.reportBrokerUpdate(ctx, result)
}

// updateLocal replaces a user-level binary in place. It starts and stops
// nothing: a Broker restarting itself does that through its service manager,
// and an installed service may have been stopped on purpose. The user has
// already confirmed the update.
func (a *app) updateLocal(ctx context.Context, current string) error {
	target, err := updateTarget()
	if err != nil {
		return err
	}
	if err := checkTargetDirWritable(filepath.Dir(target)); err != nil {
		return fmt.Errorf("%w; run \"sudo peers update\" to update a system-level installation", err)
	}

	api := a.deps.updateAPIBase()
	client := updateNewClient()
	release, err := a.deps.updateLatest(ctx, client, api)
	if err != nil {
		return err
	}
	if !a.deps.updateNewer(current, release.Version) {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is up to date\n", current)
		return nil
	}

	_, _ = fmt.Fprintf(a.stdout, "peers %s is installed; version %s is available.\n", current, release.Version)
	if release.URL != "" {
		_, _ = fmt.Fprintf(a.stdout, "Release: %s\n", release.URL)
	}

	if err := a.deps.updateApply(ctx, update.ApplyOptions{
		Target:  target,
		Version: release.Version,
		API:     api,
		Client:  client,
		GOOS:    updateGOOS,
		GOARCH:  updateGOARCH,
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(a.stdout, updateLocalTakesEffectHint)
	return nil
}

// reportBrokerUpdate handles a successful ApplyUpdate answer. When the Broker
// restarts, the command waits until the new Broker answers with the new
// version, because the request is only complete once that process is running.
func (a *app) reportBrokerUpdate(ctx context.Context, result brokerUpdateResult) error {
	from := strings.TrimPrefix(result.FromVersion, "v")
	to := strings.TrimPrefix(result.ToVersion, "v")
	if to == "" {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is up to date\n", from)
		return nil
	}
	if !result.Restarting {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is installed.\n", to)
		_, _ = fmt.Fprintln(a.stdout, updateSessionHint)
		return nil
	}

	_, _ = fmt.Fprintf(a.stdout, "The Broker is restarting to version %s.\n", to)
	if err := a.waitForBrokerVersion(ctx, to, result.UserLevel); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "The Broker is running peers %s.\n", to)
	_, _ = fmt.Fprintln(a.stdout, updateSessionHint)
	return nil
}

// waitForBrokerVersion polls UpdateStatus until the restarted Broker reports
// want. Connection errors are expected while the old process exits and the
// service manager starts the new one, so they are retried until the bound. A
// timeout names the log of the level that answered ApplyUpdate, which is where
// the failure that kept the new Broker from starting will be.
func (a *app) waitForBrokerVersion(ctx context.Context, want string, userLevel bool) error {
	want = strings.TrimPrefix(want, "v")
	deadline := time.Now().Add(updateRestartWait)
	var lastErr error
	for {
		status, err := a.deps.brokerUpdateStatus(ctx)
		switch {
		case err != nil:
			lastErr = err
		case strings.TrimPrefix(status.CurrentVersion, "v") == want:
			return nil
		default:
			lastErr = nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(updateStatusPollDelay):
		}
	}
	if lastErr != nil {
		return fmt.Errorf("the Broker did not report version %s within %s (%v); check the service log with %s", want, updateRestartWait, lastErr, updateLogHint(userLevel))
	}
	return fmt.Errorf("the Broker did not report version %s within %s; check the service log with %s", want, updateRestartWait, updateLogHint(userLevel))
}

// updateLogHint names where the service manager writes the Broker's standard
// error, for the level that answered ApplyUpdate. On macOS it also names the
// post-update log: the detached helper that reloads the service after the
// Broker exits writes its failure there, and the second phase cannot report it
// through the caller.
func updateLogHint(userLevel bool) string {
	switch {
	case updateGOOS == "darwin" && userLevel:
		return "~/Library/Logs/peers/broker.log (post-update helper: ~/Library/Logs/peers/" + service.LaunchdHelperLogName + ")"
	case updateGOOS == "darwin":
		return "/var/log/peers/broker.log (post-update helper: /var/log/peers/" + service.LaunchdHelperLogName + ")"
	case userLevel:
		return "journalctl --user -u " + service.UnitName
	default:
		return "journalctl -u " + service.UnitName
	}
}

// updateTarget returns the running executable with symbolic links resolved, so
// the replacement lands on the file the service and the shell both run.
func updateTarget() (string, error) {
	exe, err := updateExecutable()
	if err != nil {
		return "", fmt.Errorf("locating the executable: %w", err)
	}
	real, err := updateEvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving the executable %q: %w", exe, err)
	}
	return real, nil
}

// checkTargetDirWritable proves the directory will accept the temporary file
// and rename Apply needs, before anything is downloaded.
func checkTargetDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".peers-update-check-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("cannot remove the probe file in %s: %w", dir, removeErr)
	}
	return nil
}

// isTerminal reports whether r is a character device, which is how a real
// terminal reaches the process. A pipe or a redirected file is not one.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// readAnswer reads one line from r. End of input before a newline is not an
// error: the partial line still counts as the answer.
func readAnswer(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// isYes accepts the same answers from a user as the closing line of a prompt.
func isYes(answer string) bool {
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}
