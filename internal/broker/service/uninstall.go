package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Uninstall stops and removes the installed service. Configuration, identity
// and audit logs are kept. The service is stopped before its definition is
// removed, so a failed stop leaves a state a later uninstall can finish. When
// nothing is installed it succeeds and reports Removed=false, so the command
// can be repeated.
func Uninstall(opts Options) (Result, error) {
	var res Result

	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return res, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	if opts.System && opts.EUID != 0 {
		return res, errors.New("service uninstall --system must run as root")
	}
	if !opts.System && opts.EUID == 0 {
		return res, errors.New("service uninstall without --system must not run as root; the user-level unit belongs to one account")
	}

	path, err := unitPath(opts)
	if err != nil {
		return res, err
	}
	res.UnitPath = path

	switch opts.GOOS {
	case "linux":
		return uninstallLinux(opts, path, &res)
	case "darwin":
		return uninstallDarwin(opts, path, &res)
	}
	return res, nil
}

// uninstallLinux stops and disables the unit, removes its file and reloads the
// unit database. A failure before the removal keeps the file; a reload failure
// restores it, so a later uninstall can finish.
func uninstallLinux(opts Options, path string, res *Result) (Result, error) {
	rootedPath := rooted(opts, path)
	_, statErr := os.Lstat(rootedPath)
	fileExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return *res, fmt.Errorf("service: inspect %s: %w", path, statErr)
	}

	if _, err := opts.LookPath("systemctl"); err != nil {
		if fileExists {
			return *res, fmt.Errorf("service: systemctl not found, is systemd installed? %w", err)
		}
		return *res, nil
	}
	installed := fileExists || systemctlReportsInstalled(opts)
	if !installed {
		return *res, nil
	}

	// Stop and disable first. If this fails, the unit file stays for a retry,
	// because the running process may still hold the account lock.
	if err := runSystemctlCommand(opts, res, "disable", "--now", UnitName); err != nil {
		return *res, err
	}

	var content []byte
	if fileExists {
		var err error
		content, err = os.ReadFile(rootedPath)
		if err != nil {
			return *res, fmt.Errorf("service: read %s: %w", path, err)
		}
		if err := os.Remove(rootedPath); err != nil {
			return *res, fmt.Errorf("service: remove %s: %w", path, err)
		}
	}
	if err := runSystemctlCommand(opts, res, "daemon-reload"); err != nil {
		if content != nil {
			if restoreErr := writeFileAtomic(rootedPath, string(content), 0o644); restoreErr != nil {
				return *res, fmt.Errorf("%w; also could not restore %s: %v", err, path, restoreErr)
			}
		}
		return *res, err
	}

	res.Removed = true
	return *res, nil
}

// systemctlReportsInstalled reports whether systemd still has the unit enabled
// or running even though its file is gone.
func systemctlReportsInstalled(opts Options) bool {
	for _, args := range [][]string{
		{"is-enabled", UnitName},
		{"is-active", "--quiet", UnitName},
	} {
		argv := append([]string{}, args...)
		if !opts.System {
			argv = append([]string{"--user"}, argv...)
		}
		if _, err := opts.Run("systemctl", argv...); err == nil {
			return true
		}
	}
	return false
}

// uninstallDarwin boots the service out and removes its plist. A bootout
// failure other than "not loaded" keeps the plist for a retry.
func uninstallDarwin(opts Options, path string, res *Result) (Result, error) {
	domain := ""
	switch {
	case opts.System:
		domain = "system"
	case launchctlDomainExists(opts, fmt.Sprintf("gui/%d", opts.UID)):
		domain = fmt.Sprintf("gui/%d", opts.UID)
	}
	res.Domain = domain

	rootedPath := rooted(opts, path)
	_, statErr := os.Lstat(rootedPath)
	fileExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return *res, fmt.Errorf("service: inspect %s: %w", path, statErr)
	}
	if !fileExists {
		if domain == "" || !launchctlDomainLoaded(opts, domain) {
			return *res, nil
		}
	}

	if domain != "" {
		if err := bootoutService(opts, domain, res); err != nil {
			return *res, err
		}
	}
	if err := os.Remove(rootedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return *res, fmt.Errorf("service: remove %s: %w", path, err)
	}

	res.Removed = true
	return *res, nil
}

// launchctlDomainLoaded reports whether the service is loaded in domain.
func launchctlDomainLoaded(opts Options, domain string) bool {
	_, err := opts.Run("launchctl", "print", domain+"/"+Label)
	return err == nil
}
