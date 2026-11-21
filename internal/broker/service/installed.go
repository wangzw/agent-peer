package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Installed reports whether opts' level has a service: its unit file or plist
// exists, or the service manager still has it enabled or loaded. It never
// changes anything. An error means the platform is unsupported or the unit
// file or plist could not be inspected; a failing service-manager query
// counts as "not enabled or loaded", as Uninstall has always treated it.
func Installed(opts Options) (bool, error) {
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return false, fmt.Errorf("cannot check for an installed service on platform %q", opts.GOOS)
	}

	path, err := unitPath(opts)
	if err != nil {
		return false, err
	}
	fileExists, err := serviceFileExists(opts, path)
	if err != nil {
		return false, err
	}
	if fileExists {
		return true, nil
	}

	switch opts.GOOS {
	case "linux":
		return systemctlReportsInstalled(opts), nil
	case "darwin":
		// A missing domain answers the label query with an error, so asking
		// the level's own domain directly gives the same answer as checking
		// first whether the domain exists.
		domain := "system"
		if !opts.System {
			domain = fmt.Sprintf("gui/%d", opts.UID)
		}
		return launchctlDomainLoaded(opts, domain), nil
	}
	return false, nil
}

// serviceFileExists reports whether the unit file or plist at path exists
// under opts.Root. An error other than "not exist" means the path could not be
// inspected.
func serviceFileExists(opts Options, path string) (bool, error) {
	_, statErr := os.Lstat(rooted(opts, path))
	if statErr == nil {
		return true, nil
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("cannot inspect %s: %w", path, statErr)
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

// launchctlDomainLoaded reports whether the service is loaded in domain.
func launchctlDomainLoaded(opts Options, domain string) bool {
	_, err := opts.Run("launchctl", "print", domain+"/"+Label)
	return err == nil
}
