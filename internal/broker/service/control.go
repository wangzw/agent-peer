package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// ErrNotInstalled reports that opts' level has no service, as Installed judges
// it. Start, Stop and Restart return it, possibly wrapped, before running any
// service-manager command.
var ErrNotInstalled = errors.New("service: no service is installed at this level")

// Start starts opts' level service, waits for the Broker like Install and
// probes Status. A degraded Broker yields a complete Result and a
// *DegradedError; the service is left running.
func Start(opts Options) (Result, error) {
	res, domain, err := serviceTarget(opts, true)
	if err != nil {
		return res, err
	}
	res.Domain = domain

	started := time.Now()
	switch opts.GOOS {
	case "linux":
		if err := runSystemctlCommand(opts, &res, "start", UnitName); err != nil {
			return res, err
		}
	case "darwin":
		// A loaded job is started in place; bootstrapping it again would fail.
		// An unloaded job needs its label enabled first, because a failed
		// earlier installation disables it.
		if launchctlDomainLoaded(opts, domain) {
			if err := runLaunchctlCommand(opts, domain, &res, "kickstart"); err != nil {
				return res, err
			}
		} else {
			if err := enableAndBootstrap(opts, domain, &res); err != nil {
				return res, err
			}
		}
	}

	deadline, err := waitReady(opts, domain, started)
	if err != nil {
		return res, err
	}
	return res, checkDegraded(opts, deadline)
}

// Stop stops opts' level service without disabling it. On macOS it waits until
// launchd has unloaded the job, so the next Start does not race the teardown.
func Stop(opts Options) (Result, error) {
	res, domain, err := serviceTarget(opts, false)
	if err != nil {
		return res, err
	}
	res.Domain = domain

	switch opts.GOOS {
	case "linux":
		if err := runSystemctlCommand(opts, &res, "stop", UnitName); err != nil {
			return res, err
		}
	case "darwin":
		if domain == "" {
			// A user-level service can only be loaded in gui/<uid>. Without that
			// domain nothing is loaded, so stopping it is already done; the plist
			// stays installed.
			return res, nil
		}
		if err := bootoutService(opts, domain, &res); err != nil {
			return res, err
		}
		budget := stopBound(opts)
		if err := waitServiceUnloaded(opts, domain, time.Now().Add(budget), budget, unloadAdvice(opts, domain, "service stop")); err != nil {
			return res, err
		}
	}

	return res, nil
}

// Restart restarts opts' level service; on macOS it re-reads the plist.
// Readiness and the degraded check are the same as Start.
func Restart(opts Options) (Result, error) {
	res, domain, err := serviceTarget(opts, true)
	if err != nil {
		return res, err
	}
	res.Domain = domain

	started := time.Now()
	switch opts.GOOS {
	case "linux":
		if err := runSystemctlCommand(opts, &res, "restart", UnitName); err != nil {
			return res, err
		}
	case "darwin":
		// Installed accepts a loaded job whose plist was deleted. Booting that
		// job out before finding out would stop the service for good, because the
		// bootstrap that follows has no file to load. Refuse first and leave the
		// running service untouched.
		plist, err := unitPath(opts)
		if err != nil {
			return res, err
		}
		if _, statErr := os.Lstat(rooted(opts, plist)); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return res, fmt.Errorf("service: the service is loaded in %s but its plist %s is missing; run `%s` to restore it before restarting", domain, plist, installHint(opts))
			}
			return res, fmt.Errorf("service: inspect %s: %w", plist, statErr)
		}
		if err := bootoutService(opts, domain, &res); err != nil {
			return res, err
		}
		// launchctl bootout returns while the job is still being torn down. The
		// next bootstrap fails with exit 5 during that window, so wait for the
		// job to disappear before loading the plist again.
		budget := stopBound(opts)
		if err := waitServiceUnloaded(opts, domain, time.Now().Add(budget), budget, unloadAdvice(opts, domain, "service restart")); err != nil {
			return res, err
		}
		if err := enableAndBootstrap(opts, domain, &res); err != nil {
			return res, err
		}
	}

	deadline, err := waitReady(opts, domain, started)
	if err != nil {
		return res, err
	}
	return res, checkDegraded(opts, deadline)
}

// serviceTarget validates the platform and the installation and returns the
// launchd domain for macOS. needsDomain reports whether the command must load
// the job: a user-level macOS service has no domain without a graphical login
// session, and then Stop has nothing to unload.
func serviceTarget(opts Options, needsDomain bool) (Result, string, error) {
	var res Result
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return res, "", fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}

	installed, err := Installed(opts)
	if err != nil {
		return res, "", err
	}
	if !installed {
		return res, "", notInstalledError(opts)
	}
	if opts.GOOS != "darwin" {
		return res, "", nil
	}
	if opts.System {
		return res, "system", nil
	}
	domain := fmt.Sprintf("gui/%d", opts.UID)
	if launchctlDomainExists(opts, domain) {
		return res, domain, nil
	}
	if !needsDomain {
		return res, "", nil
	}
	return res, "", fmt.Errorf("service: launchd has no %s domain for uid %d; run this command from the user's graphical login session", domain, opts.UID)
}

// notInstalledError names the level that has no service, so the command can
// tell the operator which install command creates one.
func notInstalledError(opts Options) error {
	level := "user-level"
	if opts.System {
		level = "system-level"
	}
	return fmt.Errorf("%w (%s)", ErrNotInstalled, level)
}

// enableAndBootstrap enables the launchd label and loads the installed plist.
// It is what a start or restart of an unloaded job does; the enable clears a
// disabled state left by a failed earlier installation.
func enableAndBootstrap(opts Options, domain string, res *Result) error {
	path, err := unitPath(opts)
	if err != nil {
		return err
	}
	if err := runLaunchctlCommand(opts, domain, res, "enable"); err != nil {
		return err
	}

	cmdline := "launchctl bootstrap " + domain + " " + path
	res.Commands = append(res.Commands, cmdline)
	out, err := bootstrapService(opts, domain, path)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
}

// checkDegraded asks the Broker that just became ready for its status. The
// status is retried until the readiness deadline, as Install does: the Broker
// answered on the socket a moment earlier and may still be serving the call. A
// degraded Broker turns into a *DegradedError that carries the state to print;
// the service stays running.
func checkDegraded(opts Options, deadline time.Time) error {
	state, degraded, err := waitStatus(opts, deadline)
	if err != nil {
		return fmt.Errorf("service: the service is running but its status could not be read: %w; check it with `peers service status`", err)
	}
	if degraded {
		return &DegradedError{State: state}
	}
	return nil
}
