package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	systemConfigPath = "/etc/agent-peer/config.yaml"
	systemdUnitDir   = "/etc/systemd/system"
	launchDaemonDir  = "/Library/LaunchDaemons"
	systemLogDir     = "/var/log/agent-peer"
)

// Options is everything Install needs. DefaultOptions fills it from the running
// process; tests fill it by hand and set Root to a temporary directory.
type Options struct {
	System        bool
	GOOS          string                                                      // runtime.GOOS
	EUID          int                                                         // os.Geteuid()
	UID           int                                                         // os.Getuid(), used in the start hint
	Exe           string                                                      // absolute path of os.Executable()
	Home          string                                                      // account home directory
	XDGConfigHome string                                                      // XDG_CONFIG_HOME
	TempDir       string                                                      // os.TempDir()
	Root          string                                                      // filesystem prefix for tests; empty in production
	LookPath      func(file string) (string, error)                           // exec.LookPath
	Run           func(name string, args ...string) ([]byte, error)           // run a command, returning combined output
	Owner         func(path string) (uid uint32, mode fs.FileMode, err error) // owner and permission bits, not following symlinks
	EvalSymlinks  func(path string) (string, error)                           // filepath.EvalSymlinks
}

// Result describes what Install did. On error it lists the steps that already
// finished.
type Result struct {
	UnitPath   string   // written unit file or plist
	ConfigPath string   // --system only: /etc/agent-peer/config.yaml
	ConfigNew  bool     // this run created the configuration file
	Commands   []string // commands run, in order
	StartHint  string   // command that starts the service now
}

// DefaultOptions fills Options from the running process.
func DefaultOptions(system bool) (Options, error) {
	exe, err := os.Executable()
	if err != nil {
		return Options{}, fmt.Errorf("service: locate executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return Options{}, fmt.Errorf("service: make executable path absolute: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, fmt.Errorf("service: locate home directory: %w", err)
	}
	return Options{
		System:        system,
		GOOS:          runtime.GOOS,
		EUID:          os.Geteuid(),
		UID:           os.Getuid(),
		Exe:           exe,
		Home:          home,
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		TempDir:       os.TempDir(),
		LookPath:      exec.LookPath,
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		Owner: func(path string) (uint32, fs.FileMode, error) {
			fi, err := os.Lstat(path)
			if err != nil {
				return 0, 0, err
			}
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return 0, 0, fmt.Errorf("no owner information for %s", path)
			}
			return st.Uid, fi.Mode(), nil
		},
		EvalSymlinks: filepath.EvalSymlinks,
	}, nil
}

// Install performs the installation; on error the Result lists what was
// already done.
func Install(opts Options) (Result, error) {
	var res Result

	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return res, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	if opts.System && opts.EUID != 0 {
		return res, errors.New("service install --system must run as root")
	}
	if !opts.System && opts.EUID == 0 {
		return res, errors.New("service install without --system must not run as root; the user-level unit belongs to one account")
	}
	if !filepath.IsAbs(opts.Exe) {
		return res, fmt.Errorf("service: executable %q is not an absolute path", opts.Exe)
	}
	// A system unit must name the resolved executable. On macOS os.Executable
	// does not resolve symbolic links, so writing the path as given could point
	// the root-owned daemon at a link inside an account's directory, which that
	// account can retarget after the ownership check.
	if opts.System {
		real, err := opts.EvalSymlinks(opts.Exe)
		if err != nil {
			return res, fmt.Errorf("service: resolve executable %q: %w", opts.Exe, err)
		}
		opts.Exe = real
	}
	if err := checkTempBinary(opts); err != nil {
		return res, err
	}
	if opts.System {
		if err := checkSystemBinary(opts); err != nil {
			return res, err
		}
	}
	if opts.GOOS == "linux" {
		if _, err := opts.LookPath("systemctl"); err != nil {
			return res, fmt.Errorf("service: systemctl not found, is systemd installed? %w", err)
		}
	}

	unitPath, err := writeUnit(opts)
	if err != nil {
		return res, err
	}
	res.UnitPath = unitPath

	if opts.System {
		res.ConfigPath = systemConfigPath
		if err := createSystemConfig(opts, &res); err != nil {
			return res, err
		}
	}

	if opts.GOOS == "linux" {
		if err := runSystemctl(opts, &res); err != nil {
			return res, err
		}
	}

	res.StartHint = startHint(opts, unitPath)
	return res, nil
}

// configHome returns the base directory for user configuration.
func configHome(opts Options) string {
	if opts.XDGConfigHome != "" && filepath.IsAbs(opts.XDGConfigHome) {
		return opts.XDGConfigHome
	}
	return filepath.Join(opts.Home, ".config")
}

// rooted maps a production path under the test root prefix.
func rooted(opts Options, path string) string {
	if opts.Root == "" {
		return path
	}
	return filepath.Join(opts.Root, path)
}

// writeUnit writes the unit file or plist and returns its production path.
func writeUnit(opts Options) (string, error) {
	var (
		path    string
		content string
		dirs    []string
		err     error
	)
	switch {
	case opts.GOOS == "linux" && !opts.System:
		path = filepath.Join(configHome(opts), "systemd", "user", UnitName)
		content, err = SystemdUserUnit(opts.Exe)
		dirs = []string{filepath.Dir(path)}
	case opts.GOOS == "linux" && opts.System:
		path = filepath.Join(systemdUnitDir, UnitName)
		content, err = SystemdSystemUnit(opts.Exe)
		dirs = []string{systemdUnitDir}
	case opts.GOOS == "darwin" && !opts.System:
		logDir := filepath.Join(opts.Home, "Library", "Logs", "agent-peer")
		logPath := filepath.Join(logDir, "broker.log")
		path = filepath.Join(opts.Home, "Library", "LaunchAgents", Label+".plist")
		content, err = LaunchAgentPlist(opts.Exe, logPath)
		dirs = []string{logDir, filepath.Dir(path)}
	case opts.GOOS == "darwin" && opts.System:
		path = filepath.Join(launchDaemonDir, Label+".plist")
		content, err = LaunchDaemonPlist(opts.Exe)
		dirs = []string{systemLogDir, launchDaemonDir}
	}
	if err != nil {
		return "", err
	}
	for _, dir := range dirs {
		mode := fs.FileMode(0o755)
		switch {
		case opts.GOOS == "darwin" && !opts.System && dir == filepath.Join(opts.Home, "Library", "Logs", "agent-peer"):
			mode = 0o700
		case opts.GOOS == "darwin" && opts.System && dir == systemLogDir:
			// launchd opens the daemon's log files itself, so their mode is not
			// governed by the job's Umask. Keep accounts outside root's group out
			// of the directory instead; 0750 makes the files unreachable.
			mode = 0o750
		}
		if err := os.MkdirAll(rooted(opts, dir), mode); err != nil {
			return "", fmt.Errorf("service: create directory %s: %w", dir, err)
		}
	}
	if err := writeFileAtomic(rooted(opts, path), content, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// createSystemConfig creates the system configuration file if it is absent.
func createSystemConfig(opts Options, res *Result) error {
	if err := os.MkdirAll(rooted(opts, filepath.Dir(systemConfigPath)), 0o755); err != nil {
		return fmt.Errorf("service: create configuration directory: %w", err)
	}
	path := rooted(opts, systemConfigPath)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			res.ConfigNew = false
			return nil
		}
		return fmt.Errorf("service: create configuration file: %w", err)
	}
	if _, err := f.WriteString(SystemConfigTemplate()); err != nil {
		_ = f.Close()
		return fmt.Errorf("service: write configuration file: %w", err)
	}
	if opts.Root == "" {
		if err := f.Chown(0, 0); err != nil {
			_ = f.Close()
			return fmt.Errorf("service: set configuration file owner: %w", err)
		}
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("service: set configuration file mode: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("service: close configuration file: %w", err)
	}
	res.ConfigNew = true
	return nil
}

// runSystemctl reloads the unit database and enables the unit.
func runSystemctl(opts Options, res *Result) error {
	base := []string{"systemctl"}
	if !opts.System {
		base = append(base, "--user")
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}} {
		argv := append(append([]string{}, base[1:]...), args...)
		cmdline := strings.Join(append(append([]string{}, base...), args...), " ")
		res.Commands = append(res.Commands, cmdline)
		out, err := opts.Run(base[0], argv...)
		if err != nil {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
			}
			return fmt.Errorf("service: %s: %w", cmdline, err)
		}
	}
	return nil
}

// startHint returns the command that starts the service immediately.
func startHint(opts Options, unitPath string) string {
	switch {
	case opts.GOOS == "linux" && !opts.System:
		return "systemctl --user start " + UnitName
	case opts.GOOS == "linux" && opts.System:
		return "systemctl start " + UnitName
	case opts.GOOS == "darwin" && !opts.System:
		return fmt.Sprintf("launchctl bootstrap gui/%d %s", opts.UID, unitPath)
	default:
		return "launchctl bootstrap system " + unitPath
	}
}

// checkTempBinary refuses binaries under the temporary directory.
func checkTempBinary(opts Options) error {
	if opts.TempDir == "" {
		return nil
	}
	rel, err := filepath.Rel(opts.TempDir, opts.Exe)
	if err != nil {
		return nil // different volumes: not under the temporary directory
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("service: refusing to install a service for %q, which is inside the temporary directory %q", opts.Exe, opts.TempDir)
}

// checkSystemBinary verifies that the executable and every directory above it
// are owned by root and unwritable by group or others. Install resolves the
// executable first, so the check and the written unit name the same file. A
// system service must execute a file no other account can replace.
func checkSystemBinary(opts Options) error {
	for path := opts.Exe; ; {
		uid, mode, err := opts.Owner(path)
		if err != nil {
			return fmt.Errorf("service: inspect %q: %w", path, err)
		}
		switch {
		case uid != 0:
			return fmt.Errorf("service: %q is owned by uid %d, not root", path, uid)
		case mode&0o022 != 0:
			return fmt.Errorf("service: %q is writable by group or others (%#o)", path, mode.Perm())
		case mode&0o001 == 0:
			return fmt.Errorf("service: %q is not executable by other accounts", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return nil
}

// writeFileAtomic writes content to a temporary file in the target directory
// and renames it over path, replacing any existing file.
func writeFileAtomic(path, content string, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".agent-peer-install-*")
	if err != nil {
		return fmt.Errorf("service: create temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("service: set mode on %s: %w", tmp, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("service: write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("service: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("service: install %s: %w", path, err)
	}
	return nil
}
