package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Restart restarts the service installed at this deployment level and waits
// until the Broker it starts answers on the level's local socket. It reports
// false and no error when no service is installed, so the caller can tell the
// user to restart the Broker themselves.
//
// Restart uses the same start path as Install: systemd restarts the unit, and
// launchd boots the job out and bootstraps the existing plist again. Both
// paths replace the running process, which then executes the binary at the path
// the unit names; a caller that replaced that binary therefore restarts into
// the new version.
func Restart(ctx context.Context, opts Options) (bool, error) {
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return false, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	if opts.System && opts.EUID != 0 {
		return false, errors.New("service restart --system must run as root")
	}
	if !opts.System && opts.EUID == 0 {
		return false, errors.New("service restart without --system must not run as root; the user-level unit belongs to one account")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	path, err := unitPath(opts)
	if err != nil {
		return false, err
	}
	switch opts.GOOS {
	case "linux":
		return restartLinux(ctx, opts, path)
	case "darwin":
		return restartDarwin(ctx, opts, path)
	}
	return false, nil
}

// restartLinux restarts the systemd unit when the unit file or systemd itself
// reports it installed. A unit file alone is enough, because systemd may have
// lost the unit from its database while the file still names the service.
func restartLinux(ctx context.Context, opts Options, path string) (bool, error) {
	rootedPath := rooted(opts, path)
	_, statErr := os.Lstat(rootedPath)
	fileExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return false, fmt.Errorf("service: inspect %s: %w", path, statErr)
	}

	if _, err := opts.LookPath("systemctl"); err != nil {
		if !fileExists {
			return false, nil
		}
		return false, fmt.Errorf("service: systemctl not found, is systemd installed? %w", err)
	}
	if !fileExists && !systemctlReportsInstalled(opts) {
		return false, nil
	}

	var res Result
	if err := runSystemctlCommand(opts, &res, "restart", UnitName); err != nil {
		return false, err
	}
	if err := waitReadyContext(ctx, opts, ""); err != nil {
		return false, err
	}
	return true, nil
}

// restartDarwin reports whether the plist or launchd shows an installed
// service, and if so boots the job out and back in from the existing plist.
func restartDarwin(ctx context.Context, opts Options, path string) (bool, error) {
	rootedPath := rooted(opts, path)
	_, statErr := os.Lstat(rootedPath)
	fileExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return false, fmt.Errorf("service: inspect %s: %w", path, statErr)
	}

	domain := "system"
	if !opts.System {
		domain = ""
		if gui := fmt.Sprintf("gui/%d", opts.UID); launchctlDomainExists(opts, gui) {
			domain = gui
		}
	}
	loaded := domain != "" && launchctlDomainLoaded(opts, domain)
	if !fileExists && !loaded {
		return false, nil
	}
	if domain == "" {
		return false, fmt.Errorf("service: launchd has no gui/%d domain for uid %d; restart the user-level service from a graphical login session", opts.UID, opts.UID)
	}

	var res Result
	if err := runLaunchctl(opts, domain, path, &res); err != nil {
		return false, err
	}
	if err := waitReadyContext(ctx, opts, domain); err != nil {
		return false, err
	}
	return true, nil
}

// waitReadyContext reuses waitReady's readiness test and reports a cancelled
// context in place of a timeout that the cancellation caused.
func waitReadyContext(ctx context.Context, opts Options, domain string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := waitReady(opts, domain); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}
