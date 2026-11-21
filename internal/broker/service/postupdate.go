package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wangzw/peers/internal/config"
)

const (
	// LaunchdHelperLogName is the file in the level's service log directory
	// that receives the detached helper's output.
	LaunchdHelperLogName = "post-update.log"

	// launchdHelperWaitBound bounds how long the detached helper waits for the
	// Broker that answered the update to exit. The response is written before
	// that exit, so the wait only covers the Broker's graceful shutdown. When
	// the bound expires the helper reloads anyway: the bootout stops the
	// caller, which is what a wedged process needs.
	launchdHelperWaitBound = 60 * time.Second

	// processExitPollInterval separates two checks of whether the caller is
	// still running.
	processExitPollInterval = 100 * time.Millisecond
)

// PostUpdate rewrites the service definition of opts' level in this binary's
// format. Only the main unit file or plist is replaced, atomically; systemd
// drop-ins are left in place, so a definition that an operator extended keeps
// its extensions. Linux then reloads the unit database synchronously with
// daemon-reload, which does not stop the running Broker. macOS writes the plist
// and starts a detached helper instead: bootout would stop the job that runs
// the Broker and this process, so the helper performs bootout and bootstrap
// after the Broker exits. The Broker is not restarted: restarting is the update
// flow's job.
func PostUpdate(opts Options) error {
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}

	// Resolve the launchd domain before writing, so a missing domain fails
	// without leaving a half-applied definition behind.
	domain := ""
	if opts.GOOS == "darwin" {
		var err error
		domain, err = launchdDomain(opts)
		if err != nil {
			return err
		}
	}

	path, err := writeUnit(opts)
	if err != nil {
		return err
	}

	if opts.GOOS == "linux" {
		var res Result
		return runSystemctlCommand(opts, &res, "daemon-reload")
	}
	return startLaunchdReload(opts, domain, path)
}

// startLaunchdReload starts the helper that reloads the rewritten plist after
// the Broker exits. The helper is detached with setsid into a new session and
// does not inherit the Broker's standard streams, so the launchd job's bootout
// cannot stop it. Its output goes to the level's service log directory.
func startLaunchdReload(opts Options, domain, plistPath string) error {
	callerPID := os.Getppid()
	if opts.PPID != nil {
		callerPID = opts.PPID()
	}
	logPath, err := launchdHelperLogPath(opts)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(rooted(opts, logPath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("service: open the post-update log %s: %w", logPath, err)
	}

	cmd := exec.Command(opts.Exe, "internal", "launchd-reload",
		"--level", levelName(opts.System),
		"--domain", domain,
		"--plist", plistPath,
		"--pid", strconv.Itoa(callerPID),
		"--audit", auditLogPath(opts),
	)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedSysProcAttr()

	start := opts.StartDetached
	if start == nil {
		start = startDetachedProcess
	}
	startErr := start(cmd)
	closeErr := logFile.Close()
	if startErr != nil {
		return fmt.Errorf("service: start the post-update helper: %w", startErr)
	}
	if closeErr != nil {
		return fmt.Errorf("service: close the post-update log %s: %w", logPath, closeErr)
	}
	return nil
}

// startDetachedProcess starts cmd and releases it, so the caller can exit
// without waiting for the helper and the helper is reparented to launchd.
func startDetachedProcess(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// launchdHelperLogPath returns the production path of the helper's log. The
// service definition's own write already created the directory.
func launchdHelperLogPath(opts Options) (string, error) {
	switch {
	case opts.GOOS != "darwin":
		return "", fmt.Errorf("service: no launchd post-update log on %q", opts.GOOS)
	case opts.System:
		return filepath.Join(systemLogDir, LaunchdHelperLogName), nil
	case opts.Home == "":
		return "", errors.New("service: the user level needs a home directory for the post-update log")
	default:
		return filepath.Join(opts.Home, "Library", "Logs", "peers", LaunchdHelperLogName), nil
	}
}

// RelaunchOptions fills the Options the detached launchd reload helper needs.
// It runs as the same account as the service and only calls launchctl, so it
// reads neither the configuration nor the account home; its standard error is
// already the post-update log file.
func RelaunchOptions(system bool) Options {
	return Options{
		System: system,
		GOOS:   "darwin",
		EUID:   os.Geteuid(),
		UID:    os.Getuid(),
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
}

// LaunchdReload is the deferred half of a macOS post-update. The helper process
// starts it after the plist was rewritten; it waits for the Broker that
// answered the update to exit, then boots the label out and bootstraps the
// rewritten plist. The wait matters because bootout stops the job: reloading
// before the response is written would tear the Broker down while the caller is
// still waiting, and a child in the job would be stopped with it.
//
// A failure here comes after the caller received the success response, so it is
// recorded in the audit log and on standard error, which is the post-update
// log, instead of being returned to a caller.
func LaunchdReload(opts Options, domain, plistPath string, callerPID int, auditPath string) error {
	if callerPID > 0 {
		if err := waitProcessExit(opts, callerPID, launchdHelperWaitBound); err != nil {
			logf(opts, "waiting for the Broker process %d to exit: %v; reloading anyway", callerPID, err)
		}
	}
	if err := reloadLaunchd(opts, domain, plistPath); err != nil {
		if auditPath != "" {
			_ = config.AppendAudit(auditPath, config.Event{
				Event:  config.EventUpdateFailed,
				Detail: "reloading the service definition after the update failed: " + err.Error(),
			})
		}
		return err
	}
	return nil
}

// reloadLaunchd replaces the loaded job with the rewritten plist. A bootout
// that reports the job was not loaded (exit status 3) is not a failure. After
// the bootout, it waits for launchd to finish the asynchronous teardown before
// bootstrapping, and bootstrapService retries launchd's transient exit status 5
// within its own bound, as a restart does.
func reloadLaunchd(opts Options, domain, plistPath string) error {
	var res Result
	if err := bootoutService(opts, domain, &res); err != nil {
		return err
	}
	budget := stopBound(opts)
	if err := waitServiceUnloaded(opts, domain, time.Now().Add(budget), budget, unloadAdvice(opts, domain, "peers internal post-update")); err != nil {
		return err
	}

	cmdline := "launchctl bootstrap " + domain + " " + plistPath
	out, err := bootstrapService(opts, domain, plistPath)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
}

// waitProcessExit waits until pid is gone. The injected function keeps the
// helper's deferral testable without a real process.
func waitProcessExit(opts Options, pid int, bound time.Duration) error {
	if opts.WaitProcessExit != nil {
		return opts.WaitProcessExit(pid, bound)
	}
	return waitProcessGone(pid, bound)
}

// waitProcessGone polls until pid no longer exists or bound elapses.
func waitProcessGone(pid int, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for {
		if processGone(pid) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("process %d is still running after %s", pid, bound)
		}
		time.Sleep(processExitPollInterval)
	}
}

// processGone reports whether pid no longer exists. A permission error still
// means the process exists.
func processGone(pid int) bool {
	err := syscall.Kill(pid, 0)
	return errors.Is(err, syscall.ESRCH)
}

// logf writes one helper log line when the caller supplied a logger.
func logf(opts Options, format string, args ...any) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
	}
}

// levelName is the --level value of the service level.
func levelName(system bool) string {
	if system {
		return "system"
	}
	return "user"
}
