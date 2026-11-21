package service

import (
	"fmt"
	"strings"
	"time"
)

// PostUpdate rewrites the service definition of opts' level in this binary's
// format and makes the service manager read it again. Only the main unit file
// or plist is replaced, atomically; systemd drop-ins are left in place, so a
// definition that an operator extended keeps its extensions. Linux reloads the
// unit database with daemon-reload. macOS boots the job out and bootstraps the
// rewritten plist, because launchd reads ProgramArguments only at bootstrap.
// The Broker is not restarted: restarting is the update flow's job.
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
	return reloadLaunchd(opts, domain, path)
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
