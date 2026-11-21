package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wangzw/peers/internal/broker/service"
	"github.com/wangzw/peers/internal/broker/update"
)

const updateUsage = "usage: peers update [--yes]\n"

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
	updateEUID         = os.Geteuid
	updateNewClient    = func() *http.Client { return &http.Client{Transport: http.DefaultTransport} }
	updateIsTerminal   = isTerminal
	updateGOOS         = runtime.GOOS
	updateGOARCH       = runtime.GOARCH
)

func init() {
	subcommands["update"] = (*app).updateCmd
}

// updateCmd implements "peers update [--yes]". It asks the selected
// Broker to install the newest release and restart itself. A user-level Broker
// that declines (it cannot replace and restart itself) leaves the command to
// replace that account's binary in place.
func (a *app) updateCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "update without asking")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, updateUsage)
			return exitOK
		}
		_, _ = fmt.Fprint(a.stderr, updateUsage)
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprint(a.stderr, updateUsage)
		_, _ = fmt.Fprintf(a.stderr, "peers update: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	current := strings.TrimPrefix(a.version, "v")
	if !update.IsReleaseVersion(current) {
		_, _ = fmt.Fprintf(a.stderr, "peers update: version %q is not a release build; only release builds can update themselves\n", a.version)
		return exitFailure
	}

	if !*yes {
		if !updateIsTerminal(a.stdin) {
			_, _ = fmt.Fprintln(a.stderr, `peers update: standard input is not a terminal; run "peers update --yes" to update without asking`)
			return exitFailure
		}
		_, _ = fmt.Fprintf(a.stdout, "peers %s is installed. Update to the latest release? [y/N] ", current)
		answer, err := readAnswer(a.stdin)
		if err != nil {
			_, _ = fmt.Fprintf(a.stderr, "peers update: reading the answer: %v\n", err)
			return exitFailure
		}
		if !isYes(answer) {
			_, _ = fmt.Fprintln(a.stdout, "Update canceled; nothing was changed.")
			return exitOK
		}
	}

	result, err := a.deps.applyUpdateViaBroker(ctx)
	if err != nil {
		if errors.Is(err, errUserLevelUpdateUnsupported) || errors.Is(err, errNoRunningBroker) {
			return a.updateLocal(ctx, current)
		}
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitFailure
	}
	return a.reportBrokerUpdate(ctx, result)
}

// updateLocal replaces a user-level binary in place. It starts and stops
// nothing: a Broker restarting itself does that through its service manager,
// and an installed service may have been stopped on purpose. The user has
// already confirmed the update.
func (a *app) updateLocal(ctx context.Context, current string) int {
	target, err := updateTarget()
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitFailure
	}
	if err := checkTargetDirWritable(filepath.Dir(target)); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		_, _ = fmt.Fprintln(a.stderr, `peers update: run "sudo peers update" to update a system-level installation`)
		return exitFailure
	}

	api := a.deps.updateAPIBase()
	client := updateNewClient()
	release, err := a.deps.updateLatest(ctx, client, api)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitFailure
	}
	if !a.deps.updateNewer(current, release.Version) {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is up to date\n", current)
		return exitOK
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
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintln(a.stdout, updateLocalTakesEffectHint)
	return exitOK
}

// reportBrokerUpdate handles a successful ApplyUpdate answer. When the Broker
// restarts, the command waits until the new Broker answers with the new
// version, because the request is only complete once that process is running.
func (a *app) reportBrokerUpdate(ctx context.Context, result brokerUpdateResult) int {
	from := strings.TrimPrefix(result.FromVersion, "v")
	to := strings.TrimPrefix(result.ToVersion, "v")
	if to == "" {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is up to date\n", from)
		return exitOK
	}
	if !result.Restarting {
		_, _ = fmt.Fprintf(a.stdout, "peers %s is installed.\n", to)
		_, _ = fmt.Fprintln(a.stdout, updateSessionHint)
		return exitOK
	}

	_, _ = fmt.Fprintf(a.stdout, "The Broker is restarting to version %s.\n", to)
	if err := a.waitForBrokerVersion(ctx, to, result.UserLevel); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers update: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(a.stdout, "The Broker is running peers %s.\n", to)
	_, _ = fmt.Fprintln(a.stdout, updateSessionHint)
	return exitOK
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
