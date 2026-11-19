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

	"github.com/wangzw/agent-peer/internal/broker/update"
)

const updateUsage = "usage: agent-peer update [--yes]\n"

// updateSharedBinaryHint tells the user that a system-level update restarts
// only the system-level service; a user-level service that runs the same
// binary keeps the old process until it is restarted.
const updateSharedBinaryHint = "User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user."

// Test seams for the parts of the update command that reach the operating
// system. The command's GitHub requests and the service restart are reached
// through deps so an ordinary test double covers them as well.
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

// updateCmd implements "agent-peer update [--yes]". It asks GitHub for the
// latest formal release and, after the user confirms or passes --yes, replaces
// the running binary and restarts the installed service.
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
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprint(a.stderr, updateUsage)
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	current := strings.TrimPrefix(a.version, "v")
	if !update.IsReleaseVersion(current) {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: version %q is not a release build; only release builds can update themselves\n", a.version)
		return exitFailure
	}

	target, err := updateTarget()
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		return exitFailure
	}
	if err := checkTargetDirWritable(filepath.Dir(target)); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		_, _ = fmt.Fprintln(a.stderr, `agent-peer update: run "sudo agent-peer update" to update a system-level installation`)
		return exitFailure
	}

	api := a.deps.updateAPIBase()
	client := updateNewClient()
	release, err := a.deps.updateLatest(ctx, client, api)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		return exitFailure
	}
	if !a.deps.updateNewer(current, release.Version) {
		_, _ = fmt.Fprintf(a.stdout, "agent-peer %s is up to date\n", current)
		return exitOK
	}

	if !*yes && !updateIsTerminal(a.stdin) {
		_, _ = fmt.Fprintln(a.stderr, `agent-peer update: standard input is not a terminal; run "agent-peer update --yes" to update without asking`)
		return exitFailure
	}

	_, _ = fmt.Fprintf(a.stdout, "agent-peer %s is installed; version %s is available.\n", current, release.Version)
	if release.URL != "" {
		_, _ = fmt.Fprintf(a.stdout, "Release: %s\n", release.URL)
	}
	if !*yes {
		_, _ = fmt.Fprintf(a.stdout, "Update to %s? [y/N] ", release.Version)
		answer, err := readAnswer(a.stdin)
		if err != nil {
			_, _ = fmt.Fprintf(a.stderr, "agent-peer update: reading the answer: %v\n", err)
			return exitFailure
		}
		if !isYes(answer) {
			_, _ = fmt.Fprintln(a.stdout, "Update canceled; the binary and service were not changed.")
			return exitOK
		}
	}

	if err := a.deps.updateApply(ctx, update.ApplyOptions{
		Target:  target,
		Version: release.Version,
		API:     api,
		Client:  client,
		GOOS:    updateGOOS,
		GOARCH:  updateGOARCH,
	}); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(a.stdout, "agent-peer %s is installed.\n", release.Version)

	opts, err := a.deps.serviceOptions(updateEUID() == 0)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: %v\n", err)
		return exitFailure
	}
	restarted, err := a.deps.serviceRestart(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer update: restarting the service: %v\n", err)
		return exitFailure
	}
	if restarted {
		_, _ = fmt.Fprintln(a.stdout, "Restarted the agent-peer service.")
	} else {
		_, _ = fmt.Fprintln(a.stdout, "The Broker is not installed as a service; restart it to use the new version.")
	}
	_, _ = fmt.Fprintln(a.stdout, updateSharedBinaryHint)
	_, _ = fmt.Fprintln(a.stdout, "Open Claude Code sessions must reconnect with /mcp or be restarted to use the new version.")
	return exitOK
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
	f, err := os.CreateTemp(dir, ".agent-peer-update-check-*")
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
