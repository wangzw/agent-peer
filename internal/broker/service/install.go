package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wangzw/agent-peer/internal/config"
)

const (
	systemConfigPath = "/etc/agent-peer/config.yaml"
	systemdUnitDir   = "/etc/systemd/system"
	launchDaemonDir  = "/Library/LaunchDaemons"
	systemLogDir     = "/var/log/agent-peer"

	// readyBound bounds the wait for the local socket to answer after the
	// service starts. It covers the Broker's own startup and launchd's default
	// ThrottleInterval of 10 seconds, which launchd applies before relaunching
	// a process that exited too quickly.
	readyBound        = 20 * time.Second
	readyPollInterval = 100 * time.Millisecond
	// probeTimeout bounds one dial of the local socket.
	probeTimeout = 500 * time.Millisecond
)

// launchdPIDPattern matches the "pid = <n>" line of launchctl print.
var launchdPIDPattern = regexp.MustCompile(`(?m)^\s*pid = (\d+)$`)

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
	AdminGroup    string                                                      // group that administers a system-level Broker; empty uses DefaultAdminGroup
	LookPath      func(file string) (string, error)                           // exec.LookPath
	Run           func(name string, args ...string) ([]byte, error)           // run a command, returning combined output
	GroupGID      func(name string) (gid int, exists bool, err error)         // group id lookup; nil queries the platform through Run
	Owner         func(path string) (uid uint32, mode fs.FileMode, err error) // owner and permission bits, not following symlinks
	Chown         func(path string, uid, gid int) error                       // ownership setter for the configuration file; nil uses os.Chown in production
	Chmod         func(path string, mode fs.FileMode) error                   // permission setter for the configuration file; nil uses os.Chmod
	EvalSymlinks  func(path string) (string, error)                           // filepath.EvalSymlinks
	Probe         func(socketPath string) (pid int, answering bool)           // peer pid of the Broker answering on the local socket
	ListenProbe   func(addr string) error                                     // whether broker.listen can be bound; nil uses config.ProbeListen
	ReadyTimeout  time.Duration                                               // wait for the local socket after start; zero uses readyBound
}

// Result describes what Install or Uninstall did. On error it lists the steps
// that already finished.
type Result struct {
	UnitPath   string   // written or removed unit file or plist
	ConfigPath string   // configuration file for the install's level
	ConfigNew  bool     // this run created the configuration file
	AdminGroup string   // system-level: group delegated administration; empty for the user level
	Commands   []string // commands that changed state, in order
	Domain     string   // macOS: the launchd domain the service was loaded into or out of
	Removed    bool     // Uninstall: a unit file or plist was removed
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
		AdminGroup:    DefaultAdminGroup,
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
		Probe: func(socketPath string) (int, bool) {
			conn, err := net.DialTimeout("unix", socketPath, probeTimeout)
			if err != nil {
				return 0, false
			}
			defer func() { _ = conn.Close() }()
			unixConn, ok := conn.(*net.UnixConn)
			if !ok {
				return 0, true
			}
			pid, err := socketPeerPID(unixConn)
			if err != nil {
				// A Broker is answering even though its pid is unknown; report
				// it so the caller does not take it for the service's process.
				return 0, true
			}
			return pid, true
		},
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

	// The launchd domain decides both where the service is loaded and which
	// instance counts as this service's own when a Broker is already answering.
	domain := ""
	if opts.GOOS == "darwin" {
		var err error
		domain, err = launchdDomain(opts)
		if err != nil {
			return res, err
		}
	}
	if err := checkExistingBroker(opts, domain); err != nil {
		return res, err
	}

	// The administration group is resolved before any file is written, so a
	// group that cannot be created fails the installation without leaving a
	// configuration file behind.
	adminGID := -1
	if opts.System {
		gid, err := ensureAdminGroup(opts, &res)
		if err != nil {
			return res, err
		}
		adminGID = gid
		res.AdminGroup = adminGroupName(opts)
	}

	unitPath, err := writeUnit(opts)
	if err != nil {
		return res, err
	}
	res.UnitPath = unitPath

	if err := createConfig(opts, &res, adminGID); err != nil {
		return res, err
	}

	// The address is checked after the configuration exists, so the check sees
	// both a generated template and a file an earlier install left. It runs
	// before the service is registered or started, so an occupied address
	// fails the installation without leaving a service that restarts forever.
	if err := precheckListen(opts, domain, &res); err != nil {
		return res, err
	}

	switch opts.GOOS {
	case "linux":
		if err := runSystemctl(opts, &res); err != nil {
			return res, err
		}
	case "darwin":
		if err := runLaunchctl(opts, domain, unitPath, &res); err != nil {
			return res, err
		}
	}
	if err := waitReady(opts, domain); err != nil {
		return res, abandonInstall(opts, domain, &res, err)
	}

	res.Domain = domain
	return res, nil
}

// unitPath returns the production path of the unit file or plist for opts.
func unitPath(opts Options) (string, error) {
	switch {
	case opts.GOOS == "linux" && !opts.System:
		return filepath.Join(configHome(opts), "systemd", "user", UnitName), nil
	case opts.GOOS == "linux" && opts.System:
		return filepath.Join(systemdUnitDir, UnitName), nil
	case opts.GOOS == "darwin" && !opts.System:
		return filepath.Join(opts.Home, "Library", "LaunchAgents", Label+".plist"), nil
	case opts.GOOS == "darwin" && opts.System:
		return filepath.Join(launchDaemonDir, Label+".plist"), nil
	default:
		return "", fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
}

// localSocketPath returns the local socket a running Broker of this deployment
// level binds. It matches the runtime directory config picks for the level.
func localSocketPath(opts Options) string {
	if opts.System {
		if opts.GOOS == "darwin" {
			return "/var/run/agent-peer/broker.sock"
		}
		return "/run/agent-peer/broker.sock"
	}
	return fmt.Sprintf("/tmp/agent-peer-%d/broker.sock", opts.UID)
}

// checkExistingBroker refuses the installation when a Broker outside the
// agent-peer service already answers on this level's local socket. Replacing a
// service-managed Broker is fine: the start step restarts it with the new
// binary. The Broker is the service's own only when the pid the service
// manager reports equals the socket's peer pid; a loaded unit alone proves
// nothing about which process owns the socket.
func checkExistingBroker(opts Options, domain string) error {
	path := localSocketPath(opts)
	brokerPID, answering := opts.Probe(path)
	if !answering {
		return nil
	}
	servicePID, managed := serviceMainPID(opts, domain)
	if managed && servicePID == brokerPID {
		return nil
	}
	level := "user-level Broker for this account"
	if opts.System {
		level = "system-level Broker"
	}
	return fmt.Errorf("service: a %s is already answering on %s and was not started by the agent-peer service (service pid %d, socket peer pid %d); stop it before installing", level, path, servicePID, brokerPID)
}

// precheckListen refuses the installation when the address this install's
// configuration makes the Broker listen on cannot be bound. It reads the
// effective broker.listen from the configuration file, including a template
// this run just generated, and binds the address once through ListenProbe,
// which releases it again. An empty value means the Broker does not listen and
// passes without a probe.
//
// When the address is already in use, the running instance of this same
// service may own it: an install also reinstalls and restarts a service. The
// service is stopped once and the address probed again, so a reinstall is not
// mistaken for a foreign process; the start step that follows brings the
// service back with the new binary. If the address is still taken, the
// installation fails before the service is registered or started.
func precheckListen(opts Options, domain string, res *Result) error {
	path := configFilePath(opts)
	addr, err := config.ListenFromFile(rooted(opts, path))
	if err != nil {
		return fmt.Errorf("service: read broker.listen from %s: %w", path, err)
	}
	if addr == "" {
		return nil
	}
	probe := opts.ListenProbe
	if probe == nil {
		probe = config.ProbeListen
	}
	err = probe(addr)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return listenError(path, addr, err)
	}

	stopForPrecheck(opts, domain, res)
	deadline := time.Now().Add(time.Second)
	for {
		if probe(addr) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return listenError(path, addr, err)
		}
		time.Sleep(readyPollInterval / 2)
	}
}

// stopForPrecheck stops the service during the listen precheck. A service that
// is not installed or not running makes the command fail; the second probe
// decides whether the address is still taken, so that failure is not reported
// here. The call is recorded in res.Commands with the other state changes.
func stopForPrecheck(opts Options, domain string, res *Result) {
	switch opts.GOOS {
	case "linux":
		_ = runSystemctlCommand(opts, res, "stop", UnitName)
	case "darwin":
		_ = bootoutService(opts, domain, res)
	}
}

// listenError explains why the installation cannot use this install's
// broker.listen and what the operator can change.
func listenError(path, addr string, err error) error {
	advice := "change broker.listen to another port, or leave it empty to run without a TCP listener, and run service install again"
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("service: broker.listen %q from %s is already in use; %s", addr, path, advice)
	}
	return fmt.Errorf("service: cannot bind broker.listen %q from %s: %w; %s", addr, path, err, advice)
}

// abandonInstall stops and unregisters the service a failed install started.
// The unit or plist and the configuration file are kept, so fixing the cause
// and running service install again finds them. The service is stopped rather
// than left to restart on every launchd or systemd cycle.
func abandonInstall(opts Options, domain string, res *Result, cause error) error {
	var cleanup error
	switch opts.GOOS {
	case "linux":
		cleanup = runSystemctlCommand(opts, res, "disable", "--now", UnitName)
	case "darwin":
		cleanup = bootoutService(opts, domain, res)
	}
	if cleanup != nil {
		return fmt.Errorf("%w; the service could not be stopped and uninstalled: %v; fix the problem and run service install again", cause, cleanup)
	}
	return fmt.Errorf("%w; the service was stopped and uninstalled; fix the problem and run service install again", cause)
}

// serviceMainPID returns the pid the service manager reports for the unit and
// whether it reports a running one.
func serviceMainPID(opts Options, domain string) (int, bool) {
	switch opts.GOOS {
	case "linux":
		args := []string{"show", "--property", "MainPID", "--value", UnitName}
		if !opts.System {
			args = append([]string{"--user"}, args...)
		}
		out, err := opts.Run("systemctl", args...)
		if err != nil {
			return 0, false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil || pid == 0 {
			return 0, false
		}
		return pid, true
	case "darwin":
		out, err := opts.Run("launchctl", "print", domain+"/"+Label)
		if err != nil {
			return 0, false
		}
		match := launchdPIDPattern.FindStringSubmatch(string(out))
		if match == nil {
			return 0, false
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid == 0 {
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

// launchdDomain picks the launchd domain for a user-level service. launchd
// exposes gui/<uid> only to an account with a graphical login session. Probe
// 44498306091 showed that user/<uid> accepts print but rejects bootstrap with
// exit 5, so installing there would leave a loaded-looking, stopped service;
// the installation refuses before writing anything instead.
func launchdDomain(opts Options) (string, error) {
	if opts.System {
		return "system", nil
	}
	gui := fmt.Sprintf("gui/%d", opts.UID)
	if launchctlDomainExists(opts, gui) {
		return gui, nil
	}
	return "", fmt.Errorf("service: launchd has no %s domain for uid %d; install the user-level service from a graphical login session, or use --system on a host without one", gui, opts.UID)
}

// launchctlDomainExists reports whether launchd knows the domain.
func launchctlDomainExists(opts Options, domain string) bool {
	_, err := opts.Run("launchctl", "print", domain)
	return err == nil
}

// runLaunchctl replaces the loaded service, if any, with the freshly written
// plist. A failed bootstrap removes the plist this run wrote, so a failed
// installation leaves no installed but unloaded service behind.
func runLaunchctl(opts Options, domain, plistPath string, res *Result) error {
	if err := bootoutService(opts, domain, res); err != nil {
		return err
	}

	cmdline := "launchctl bootstrap " + domain + " " + plistPath
	res.Commands = append(res.Commands, cmdline)
	out, err := opts.Run("launchctl", "bootstrap", domain, plistPath)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if rmErr := os.Remove(rooted(opts, plistPath)); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			if msg != "" {
				return fmt.Errorf("service: %s: %w: %s; also could not remove %s: %v", cmdline, err, msg, plistPath, rmErr)
			}
			return fmt.Errorf("service: %s: %w; also could not remove %s: %v", cmdline, err, plistPath, rmErr)
		}
		if msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
}

// bootoutService unloads the service before a replacement or removal. launchctl
// exits 3 with "No such process" when the service is not loaded (probe
// 44498306091); that is the only bootout failure ignored. Any other failure is
// returned, because the old service may still own the account lock or the
// socket.
func bootoutService(opts Options, domain string, res *Result) error {
	target := domain + "/" + Label
	cmdline := "launchctl bootout " + target
	res.Commands = append(res.Commands, cmdline)
	out, err := opts.Run("launchctl", "bootout", target)
	if err == nil {
		return nil
	}
	if exitStatus(err) == 3 {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
	}
	return fmt.Errorf("service: %s: %w", cmdline, err)
}

// exitStatus returns the exit code of a command error, or -1 when the error
// did not come from an exited process.
func exitStatus(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// waitReady waits until the Broker answering on this level's local socket is
// the service's own process. It applies the same test as the pre-install
// check: the service manager's pid must equal the socket peer pid. An answer
// from any other process is not readiness, because the service cannot take over
// that process's account lock. A timeout reports the log location, since the
// service manager owns the process and its standard error.
func waitReady(opts Options, domain string) error {
	bound := opts.ReadyTimeout
	if bound <= 0 {
		bound = readyBound
	}
	path := localSocketPath(opts)
	deadline := time.Now().Add(bound)
	var (
		peerPID    int
		answering  bool
		servicePID int
		managed    bool
	)
	for {
		peerPID, answering = opts.Probe(path)
		if answering {
			servicePID, managed = serviceMainPID(opts, domain)
			if managed && servicePID == peerPID {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if answering {
				return fmt.Errorf("service: the Broker answering on %s is not the service's process (service pid %d, socket peer pid %d); stop it and check the service log with %s", path, servicePID, peerPID, logHint(opts))
			}
			// The precheck can pass and the Broker still fail to start; the
			// service log records the cause. Include it so the timeout names
			// the real problem instead of only the silent socket.
			reason := startupFailureReason(opts)
			if reason != "" {
				return fmt.Errorf("service: the Broker did not answer on %s within %s; check the service log with %s; its last startup_failed record says: %s", path, bound, logHint(opts), reason)
			}
			return fmt.Errorf("service: the Broker did not answer on %s within %s; check the service log with %s", path, bound, logHint(opts))
		}
		time.Sleep(readyPollInterval)
	}
}

// logHint names where the service manager writes the Broker's standard error.
func logHint(opts Options) string {
	if path := logFile(opts); path != "" {
		return path
	}
	if opts.System {
		return "journalctl -u " + UnitName
	}
	return "journalctl --user -u " + UnitName
}

// logFile returns the service log file macOS launchd writes, or an empty
// string on a platform whose service manager reads the log from journald.
func logFile(opts Options) string {
	if opts.GOOS != "darwin" {
		return ""
	}
	if opts.System {
		return "/var/log/agent-peer/broker.log"
	}
	return filepath.Join(opts.Home, "Library", "Logs", "agent-peer", "broker.log")
}

// startupFailureReason returns the error of the last startup_failed record in
// the service log, or an empty string when there is none or the log cannot be
// read. It never fails an installation: the record is only additional context
// for a readiness timeout.
func startupFailureReason(opts Options) string {
	if path := logFile(opts); path != "" {
		data, err := readLogTail(rooted(opts, path))
		if err != nil {
			return ""
		}
		return lastStartupFailure(string(data))
	}
	args := []string{"-u", UnitName, "-n", "100", "--no-pager", "-o", "cat"}
	if !opts.System {
		args = append([]string{"--user"}, args...)
	}
	out, err := opts.Run("journalctl", args...)
	if err != nil {
		return ""
	}
	return lastStartupFailure(string(out))
}

// logTailBytes bounds how much of the service log startupFailureReason reads
// from the end. The record that matters is the last one, and the log may hold
// weeks of runtime records.
const logTailBytes = 64 << 10

// readLogTail returns at most the last logTailBytes of the file. A cut line at
// the start is harmless because lastStartupFailure ignores records it cannot
// parse.
func readLogTail(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if start := info.Size() - logTailBytes; start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

// lastStartupFailure scans the log from the end and returns the error field of
// the last startup_failed JSON record.
func lastStartupFailure(log string) string {
	lines := strings.Split(log, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, `"startup_failed"`) {
			continue
		}
		var record struct {
			Event string `json:"event"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record.Event == "startup_failed" && record.Error != "" {
			return record.Error
		}
	}
	return ""
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
	path, err := unitPath(opts)
	if err != nil {
		return "", err
	}
	var (
		content string
		dirs    []string
	)
	switch {
	case opts.GOOS == "linux" && !opts.System:
		content, err = SystemdUserUnit(opts.Exe)
		dirs = []string{filepath.Dir(path)}
	case opts.GOOS == "linux" && opts.System:
		content, err = SystemdSystemUnit(opts.Exe)
		dirs = []string{systemdUnitDir}
	case opts.GOOS == "darwin" && !opts.System:
		logDir := filepath.Join(opts.Home, "Library", "Logs", "agent-peer")
		logPath := filepath.Join(logDir, "broker.log")
		content, err = LaunchAgentPlist(opts.Exe, logPath)
		dirs = []string{logDir, filepath.Dir(path)}
	case opts.GOOS == "darwin" && opts.System:
		content, err = LaunchDaemonPlist(opts.Exe)
		dirs = []string{systemLogDir, launchDaemonDir}
	default:
		return "", fmt.Errorf("service: unsupported platform %q", opts.GOOS)
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

// configFilePath returns the production path of the configuration file for the
// install's level.
func configFilePath(opts Options) string {
	if opts.System {
		return systemConfigPath
	}
	return filepath.Join(opts.Home, ".agent-peer", "config.yaml")
}

// createConfig creates the configuration file for the install's level if it is
// absent and reports what happened in res. The file is created exclusively, so
// an existing file is never modified: its content, group and permissions stay
// as they are. The system-level file belongs to root and to the administration
// group, mode 0660; the user-level file belongs to the installing account, mode
// 0600. The content is the template the config package generates for the level.
// adminGID is the administration group for the system level and ignored
// otherwise.
func createConfig(opts Options, res *Result, adminGID int) error {
	fileMode := fs.FileMode(0o600)
	if opts.System {
		fileMode = 0o660
	}
	return createConfigWith(opts, res, adminGID, fileMode, func(path string) (*os.File, error) {
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	})
}

// createConfigWith implements createConfig; fileMode is the mode the new file
// must end up with, and open lets a test force a write failure after the file
// was created.
func createConfigWith(opts Options, res *Result, adminGID int, fileMode fs.FileMode, open func(string) (*os.File, error)) error {
	path := configFilePath(opts)
	res.ConfigPath = path

	dirMode := fs.FileMode(0o755)
	if !opts.System {
		// The user-level file lives in the account's data directory, which the
		// config package keeps private to the account.
		dirMode = 0o700
	}
	if err := os.MkdirAll(rooted(opts, filepath.Dir(path)), dirMode); err != nil {
		return fmt.Errorf("service: create configuration directory: %w", err)
	}

	fsPath := rooted(opts, path)
	f, err := open(fsPath)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			res.ConfigNew = false
			return nil
		}
		return fmt.Errorf("service: create configuration file: %w", err)
	}
	// A file this run created is removed when a later step fails, so the next
	// install does not mistake a partial file for an existing configuration.
	removePartial := func() {
		_ = os.Remove(fsPath)
	}
	mode := config.ModeUser
	if opts.System {
		mode = config.ModeSystem
	}
	if _, err := f.Write(config.Template(mode)); err != nil {
		_ = f.Close()
		removePartial()
		return fmt.Errorf("service: write configuration file: %w", err)
	}
	if opts.System {
		if err := setConfigOwner(opts, fsPath, adminGID); err != nil {
			_ = f.Close()
			removePartial()
			return fmt.Errorf("service: set configuration file owner: %w", err)
		}
	}
	if err := setConfigMode(opts, fsPath, fileMode); err != nil {
		_ = f.Close()
		removePartial()
		return fmt.Errorf("service: set configuration file mode: %w", err)
	}
	if err := f.Close(); err != nil {
		removePartial()
		return fmt.Errorf("service: close configuration file: %w", err)
	}
	res.ConfigNew = true
	return nil
}

// setConfigOwner makes the system configuration file root-owned and gives it
// the administration group. Options.Chown replaces the operation in a test; a
// test that roots the filesystem and supplies no Chown cannot change ownership
// without root, so the step is skipped there.
func setConfigOwner(opts Options, path string, gid int) error {
	if opts.Chown != nil {
		return opts.Chown(path, 0, gid)
	}
	if opts.Root != "" {
		return nil
	}
	return os.Chown(path, 0, gid)
}

// setConfigMode sets the configuration file's permission bits, through
// Options.Chmod when a test supplies it.
func setConfigMode(opts Options, path string, mode fs.FileMode) error {
	if opts.Chmod != nil {
		return opts.Chmod(path, mode)
	}
	return os.Chmod(path, mode)
}

// runSystemctl reloads the unit database, enables the unit and starts it.
// restart also starts a unit that is not running yet, and replaces the process
// when the unit was already running from an older binary.
func runSystemctl(opts Options, res *Result) error {
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}, {"restart", UnitName}} {
		if err := runSystemctlCommand(opts, res, args...); err != nil {
			return err
		}
	}
	return nil
}

// runSystemctlCommand runs one systemctl command, records it and reports its
// output on failure.
func runSystemctlCommand(opts Options, res *Result, args ...string) error {
	argv := append([]string{}, args...)
	if !opts.System {
		argv = append([]string{"--user"}, argv...)
	}
	cmdline := strings.Join(append([]string{"systemctl"}, argv...), " ")
	res.Commands = append(res.Commands, cmdline)
	out, err := opts.Run("systemctl", argv...)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
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
