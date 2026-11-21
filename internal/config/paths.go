package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// resolvePaths returns the path set for the account and deployment mode. It
// does not create anything.
func (c *core) resolvePaths() (PathSet, error) {
	euid := c.euid()
	runtimeDir := runtimeDirPath(euid)
	switch c.mode {
	case ModeUser:
		home, err := c.homeDir()
		if err != nil {
			return PathSet{}, fmt.Errorf("config: cannot determine the home directory: %w", err)
		}
		if home == "" {
			return PathSet{}, errors.New("config: the account has no home directory")
		}
		dataDir := filepath.Join(home, ".peers")
		return PathSet{
			RuntimeDir: runtimeDir,
			ConfigFile: filepath.Join(dataDir, "config.yaml"),
			DataDir:    dataDir,
			AuditLog:   filepath.Join(dataDir, "audit.log"),
		}, nil
	case ModeSystem:
		if c.systemPaths != nil {
			return c.systemPaths(), nil
		}
		return PathSet{
			RuntimeDir: runtimeDir,
			ConfigFile: "/etc/peers/config.yaml",
			DataDir:    "/var/lib/peers",
			AuditLog:   "/var/log/peers/audit.log",
		}, nil
	default:
		return PathSet{}, fmt.Errorf("config: unknown deployment mode %d", c.mode)
	}
}

// runtimeRoot returns the runtime directory of one account. It is the seam
// tests use to place the directory under a temporary root; production code
// never replaces it.
var runtimeRoot = func(euid int) string {
	if euid == 0 {
		if runtime.GOOS == "darwin" {
			return "/var/run/peers"
		}
		return "/run/peers"
	}
	return fmt.Sprintf("/tmp/peers-%d", euid)
}

// runtimeDirPath depends only on the effective account.
func runtimeDirPath(euid int) string {
	return runtimeRoot(euid)
}

func accountLockPath(euid int) string {
	return filepath.Join(runtimeDirPath(euid), "broker.lock")
}

// ensureRuntimeDir creates and validates the runtime directory. It returns the
// path. The caller owns the directory after this call.
func ensureRuntimeDir(euid int) (string, error) {
	path := runtimeDirPath(euid)
	mode := os.FileMode(0o700)
	if euid == 0 {
		mode = 0o755
	}
	if err := ensureDir(path, mode, euid, permRuntime); err != nil {
		return "", err
	}
	return path, nil
}

type permRule int

const (
	permRuntime permRule = iota // root: no group/other write; otherwise no group/other access
	permData                    // no group/other access
	permShared                  // no group/other write
)

func ensureDir(path string, mode os.FileMode, euid int, rule permRule) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("config: cannot create %s: %w", path, err)
	}
	if err := validateDir(path, euid, rule); err != nil {
		return err
	}
	return nil
}

func validateDir(path string, euid int, rule permRule) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("config: cannot inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config: %s is a symbolic link", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("config: %s is not a directory", path)
	}
	f, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("config: cannot open %s: %w", path, err)
	}
	file := os.NewFile(uintptr(f), path)
	st, err := file.Stat()
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("config: cannot inspect %s: %w", path, err)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != euid {
		return fmt.Errorf("config: %s is owned by uid %d, not %d", path, int(sys.Uid), euid)
	}
	if err := checkDirPermission(st.Mode().Perm(), euid, rule); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	return nil
}

// checkDirPermission applies the permission rule for one kind of directory.
// A rule allows tightening the mode but never loosening it, and the root
// runtime directory only forbids write access for group and other.
func checkDirPermission(perm os.FileMode, euid int, rule permRule) error {
	switch rule {
	case permRuntime:
		if euid == 0 {
			if perm&0o022 != 0 {
				return errors.New("must not be group or world writable")
			}
		} else if perm&0o077 != 0 {
			return errors.New("must not be accessible by group or other")
		}
	case permData:
		if perm&0o077 != 0 {
			return errors.New("must not be accessible by group or other")
		}
	case permShared:
		if perm&0o022 != 0 {
			return errors.New("must not be group or world writable")
		}
	}
	return nil
}

// prepareDataDirs creates and validates the directories holding the
// configuration file and audit log.
func (c *core) prepareDataDirs(paths PathSet) error {
	euid := c.euid()
	switch c.mode {
	case ModeUser:
		if err := ensureDir(paths.DataDir, 0o700, euid, permData); err != nil {
			return err
		}
	case ModeSystem:
		if euid != 0 {
			return errors.New("config: a system Broker must run as root")
		}
		if err := ensureDir(filepath.Dir(paths.ConfigFile), 0o755, euid, permShared); err != nil {
			return err
		}
		if err := ensureDir(paths.DataDir, 0o700, euid, permData); err != nil {
			return err
		}
		if err := ensureDir(filepath.Dir(paths.AuditLog), 0o755, euid, permShared); err != nil {
			return err
		}
	}
	return nil
}

var accountInit struct {
	mu  sync.Mutex
	err error
}

func takeAccountInitErr() error {
	accountInit.mu.Lock()
	defer accountInit.mu.Unlock()
	err := accountInit.err
	accountInit.err = nil
	return err
}

func setAccountInitErr(err error) {
	accountInit.mu.Lock()
	defer accountInit.mu.Unlock()
	accountInit.err = err
}

// CheckRuntimeDir creates and checks the runtime directory, so the broker can
// check it before taking the account lock.
func CheckRuntimeDir() error {
	return checkRuntimeDir(os.Geteuid())
}

func checkRuntimeDir(euid int) error {
	_, err := ensureRuntimeDir(euid)
	return err
}

// AccountLock returns the path of the account lock. It tries to create and
// check the runtime directory; a failure is remembered and returned by the next
// Load.
func AccountLock() string {
	euid := os.Geteuid()
	if _, err := ensureRuntimeDir(euid); err != nil {
		setAccountInitErr(err)
	}
	return accountLockPath(euid)
}
