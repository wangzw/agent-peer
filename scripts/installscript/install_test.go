package installscript

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// installScriptPath is the script under test, relative to this package.
const installScriptPath = "../install.sh"

// systemPath is a fixed search path for the real tools the script needs. It
// deliberately omits the usual locations of claude, which live under $HOME or
// /usr/local, so a test that needs claude missing can drop the fake bin
// directory and still run every standard tool.
var systemPath = strings.Join([]string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}, string(os.PathListSeparator))

// fixture is one extracted archive plus the fake command directory and the
// environment the script runs with.
type fixture struct {
	t     *testing.T
	base  string
	home  string
	fakes string

	archive string
	script  string

	agentLog     string
	pathAgentLog string
	claudeLog    string
	claudeState  string
	xattrLog     string
	installLog   string
	orderLog     string
	groupLog     string
	sudoLog      string
}

// newFixture builds an archive root with install.sh and a fake agent-peer, and
// a fake bin directory holding every command the tests replace.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:            t,
		base:         base,
		home:         filepath.Join(base, "home"),
		fakes:        filepath.Join(base, "fakebin"),
		archive:      filepath.Join(base, "archive"),
		agentLog:     filepath.Join(base, "agent.log"),
		pathAgentLog: filepath.Join(base, "path-agent.log"),
		claudeLog:    filepath.Join(base, "claude.log"),
		claudeState:  filepath.Join(base, "claude-state"),
		xattrLog:     filepath.Join(base, "xattr.log"),
		installLog:   filepath.Join(base, "install.log"),
		orderLog:     filepath.Join(base, "order.log"),
		groupLog:     filepath.Join(base, "group.log"),
		sudoLog:      filepath.Join(base, "sudo.log"),
	}
	for _, dir := range []string{f.home, f.fakes, f.archive} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	realInstall := mustLookPath(t, "install")
	realID := mustLookPath(t, "id")
	realUname := mustLookPath(t, "uname")

	source, err := filepath.Abs(installScriptPath)
	if err != nil {
		t.Fatalf("resolving %s: %v", installScriptPath, err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("reading %s: %v", source, err)
	}
	f.script = filepath.Join(f.archive, "install.sh")
	writeExecutable(t, f.script, string(data))
	writeExecutable(t, filepath.Join(f.archive, "agent-peer"), fakeAgentPeer)

	writeExecutable(t, filepath.Join(f.fakes, "claude"), fakeClaude)
	writeExecutable(t, filepath.Join(f.fakes, "agent-peer"), fakePathAgentPeer)
	writeExecutable(t, filepath.Join(f.fakes, "stat"), fakeStat)
	writeExecutable(t, filepath.Join(f.fakes, "id"), strings.ReplaceAll(fakeID, "@REAL_ID@", shellQuote(realID)))
	writeExecutable(t, filepath.Join(f.fakes, "uname"), strings.ReplaceAll(fakeUname, "@REAL_UNAME@", shellQuote(realUname)))
	writeExecutable(t, filepath.Join(f.fakes, "xattr"), fakeXattr)
	writeExecutable(t, filepath.Join(f.fakes, "install"), strings.ReplaceAll(fakeInstall, "@REAL_INSTALL@", shellQuote(realInstall)))
	writeExecutable(t, filepath.Join(f.fakes, "sudo"), fakeSudo)
	writeExecutable(t, filepath.Join(f.fakes, "usermod"), fakeUsermod)
	writeExecutable(t, filepath.Join(f.fakes, "dseditgroup"), fakeDseditgroup)
	return f
}

// environ returns the environment the script runs with. overrides replace or
// add an environment variable; a nil map keeps the defaults.
func (f *fixture) environ(overrides map[string]string) []string {
	env := map[string]string{
		"HOME":                    f.home,
		"PATH":                    f.fakes + string(os.PathListSeparator) + systemPath,
		"LC_ALL":                  "C",
		"FAKE_AGENT_LOG":          f.agentLog,
		"FAKE_AGENT_VERSION":      "agent-peer test-version",
		"FAKE_AGENT_STATUS":       "ok",
		"FAKE_AGENT_SERVICE_EXIT": "0",
		"FAKE_PATH_AGENT_LOG":     f.pathAgentLog,
		"FAKE_CLAUDE_LOG":         f.claudeLog,
		"FAKE_CLAUDE_STATE":       f.claudeState,
		"FAKE_STAT_OWNER":         "0",
		"FAKE_STAT_MODE":          "755",
		"FAKE_STAT_REJECT_GNU":    "0",
		"FAKE_INSTALL_COPY":       "0",
		"FAKE_INSTALL_LOG":        f.installLog,
		"FAKE_XATTR_LOG":          f.xattrLog,
		"FAKE_XATTR_EXIT":         "0",
		"FAKE_ORDER_LOG":          f.orderLog,
		"FAKE_GROUP_LOG":          f.groupLog,
		"FAKE_GROUP_EXIT":         "0",
		"FAKE_SUDO_LOG":           f.sudoLog,
	}
	for key, value := range overrides {
		env[key] = value
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environ := make([]string, 0, len(keys))
	for _, key := range keys {
		environ = append(environ, key+"="+env[key])
	}
	return environ
}

// run executes the copied install.sh with the fixture environment in a new
// session without a controlling terminal. The level prompt then has no terminal
// to read, so a test that runs without --system or --user exercises the
// no-terminal path instead of blocking on the terminal that runs the tests.
func (f *fixture) run(overrides map[string]string, args ...string) (stdout, stderr string, code int) {
	f.t.Helper()
	cmd := exec.Command("sh", append([]string{f.script}, args...)...)
	cmd.Dir = f.archive
	cmd.Env = f.environ(overrides)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			f.t.Fatalf("running install.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), code
}

// runDetached runs install.sh in a new session without a controlling terminal,
// so /dev/tty cannot be opened. stdin holds stdin for the script; a prompt that
// reads stdin instead of /dev/tty would see it.
func (f *fixture) runDetached(stdin string, overrides map[string]string, args ...string) (stdout, stderr string, code int) {
	f.t.Helper()
	cmd := exec.Command("sh", append([]string{f.script}, args...)...)
	cmd.Dir = f.archive
	cmd.Env = f.environ(overrides)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			f.t.Fatalf("running install.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), code
}

// ttyTimeout bounds one pseudo-terminal run. The install waits for the service
// to answer, and a loaded CI runner can take several seconds.
const ttyTimeout = time.Minute

// runWithTTY runs install.sh under a pseudo-terminal, so /dev/tty exists.
// answer is typed on that terminal while the script's stdin is /dev/null, which
// means the script can only learn the answer by reading /dev/tty. stdout and
// stderr go to files so they stay separate from the terminal conversation,
// which runWithTTY returns as tty.
func (f *fixture) runWithTTY(t *testing.T, answer string, overrides map[string]string, args ...string) (stdout, stderr, tty string, code int) {
	t.Helper()
	scriptPath, err := exec.LookPath("script")
	if err != nil {
		t.Fatalf("script(1) is required to run the prompt tests: %v", err)
	}

	outPath := filepath.Join(f.base, "tty-stdout")
	errPath := filepath.Join(f.base, "tty-stderr")
	statusPath := filepath.Join(f.base, "tty-status")
	inner := "sh " + shellQuote(f.script)
	for _, arg := range args {
		inner += " " + shellQuote(arg)
	}
	inner += " </dev/null >" + shellQuote(outPath) + " 2>" + shellQuote(errPath)
	inner += "; printf '%s' \"$?\" >" + shellQuote(statusPath)

	ctx, cancel := context.WithTimeout(t.Context(), ttyTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, scriptPath, "-q", "/dev/null", "sh", "-c", inner)
	} else {
		cmd = exec.CommandContext(ctx, scriptPath, "-q", "-c", inner, "/dev/null")
	}
	cmd.Dir = f.archive
	cmd.Env = f.environ(overrides)
	cmd.Stdin = strings.NewReader(answer)
	var terminal strings.Builder
	cmd.Stdout = &terminal
	cmd.Stderr = &terminal
	// The exit status travels through statusPath because script(1) does not
	// report the command's status the same way on every platform.
	_ = cmd.Run()

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("install.sh recorded no exit status under the terminal: %v\nterminal output:\n%s", err, terminal.String())
	}
	code, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parsing the recorded exit status %q: %v", data, err)
	}
	return readFile(t, outPath), readFile(t, errPath), terminal.String(), code
}

// systemPrefix creates the install directory a system-level test installs into.
func (f *fixture) systemPrefix(t *testing.T) string {
	t.Helper()
	prefix := filepath.Join(f.base, "sysbin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatalf("creating %s: %v", prefix, err)
	}
	return prefix
}

// systemInstallEnv returns overrides for a system-level install that passes the
// directory checks with the fake stat. sudoUser is set only when non-empty, and
// the fake group listing then contains just that account, so the account is not
// yet in the admin group.
func (f *fixture) systemInstallEnv(sudoUser string) map[string]string {
	env := map[string]string{
		"FAKE_ID_UID":       "0",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
		"FAKE_GROUP_LOG":    f.groupLog,
	}
	if sudoUser != "" {
		env["SUDO_USER"] = sudoUser
		env["FAKE_ID_GROUPS"] = sudoUser
	}
	return env
}

// systemInstallArgs returns the arguments for a system-level install that
// installs the service and skips MCP registration.
func systemInstallArgs(prefix string, extra ...string) []string {
	args := []string{"--system", "--no-mcp", "--prefix", prefix}
	return append(args, extra...)
}

// pathUnderHome returns an absolute path under the home directory as the
// script sees it. The user-level prefix keeps the caller's spelling rather
// than a resolved physical path, so PATH comparisons and registered command
// paths match what the user wrote.
func (f *fixture) pathUnderHome(t *testing.T, elems ...string) string {
	t.Helper()
	if !filepath.IsAbs(f.home) {
		t.Fatalf("the fixture home %s is not absolute", f.home)
	}
	return filepath.Join(append([]string{f.home}, elems...)...)
}

// userBinary returns the installed binary path for a user-level prefix, which
// keeps the caller's spelling.
func (f *fixture) userBinary(dir string) string {
	return filepath.Join(dir, "agent-peer")
}

// installedBinary returns the installed binary path for a prefix directory
// that exists, resolving symlinks the same way the script does.
func (f *fixture) installedBinary(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	return filepath.Join(resolved, "agent-peer")
}

// resolvedArchive returns the archive root after the same symlink resolution
// the script applies to its own directory.
func (f *fixture) resolvedArchive(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(f.archive)
	if err != nil {
		t.Fatalf("resolving %s: %v", f.archive, err)
	}
	return resolved
}

func TestInstallScriptHelpAndUsageErrors(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		f := newFixture(t)
		stdout, _, code := f.run(nil, "--help")
		if code != 0 {
			t.Fatalf("--help exit = %d, want 0", code)
		}
		for _, want := range []string{
			"--system", "--user", "--prefix <dir>", "--no-service", "--no-mcp",
			"--admin-group <name>", "--join-admin-group", "--no-admin-group",
			"agent-peer uninstall",
		} {
			mustContain(t, "help output", stdout, want)
		}
	})

	t.Run("admin group without value", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.run(nil, "--admin-group")
		if code != 2 {
			t.Fatalf("--admin-group without a value exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "--admin-group needs a group name")
	})

	t.Run("empty admin group", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.run(nil, "--admin-group=")
		if code != 2 {
			t.Fatalf("--admin-group= exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "--admin-group needs a non-empty group name")
	})

	t.Run("admin group option without system", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.runDetached("", nil, "--join-admin-group")
		if code != 2 {
			t.Fatalf("--join-admin-group without --system exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "only valid with --system")
	})

	t.Run("unknown argument", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.run(nil, "--bogus")
		if code != 2 {
			t.Fatalf("unknown argument exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "unknown argument")
		mustContain(t, "stderr", stderr, "usage:")
	})

	t.Run("missing prefix value", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.run(nil, "--prefix")
		if code != 2 {
			t.Fatalf("--prefix without a value exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "--prefix needs a directory")
	})

	t.Run("empty prefix value", func(t *testing.T) {
		f := newFixture(t)
		_, stderr, code := f.run(nil, "--prefix=")
		if code != 2 {
			t.Fatalf("--prefix= exit = %d, want 2", code)
		}
		mustContain(t, "stderr", stderr, "--prefix needs a non-empty directory")
	})
}

func TestInstallScriptUserInstall(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := f.run(nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	binary := f.pathUnderHome(t, ".local", "bin", "agent-peer")
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("the installed binary is missing: %v", err)
	}

	agent := readLines(t, f.agentLog)
	if got := countLines(agent, "service install"); got != 1 {
		t.Errorf("service install calls = %d, want 1; calls: %q", got, agent)
	}
	if got := countLines(agent, "--version"); got != 1 {
		t.Errorf("--version calls = %d, want 1; calls: %q", got, agent)
	}
	if got := countLines(agent, "status"); got != 1 {
		t.Errorf("status calls = %d, want 1; calls: %q", got, agent)
	}

	claude := readLines(t, f.claudeLog)
	if got := countLines(claude, "mcp get peer"); got != 1 {
		t.Errorf("mcp get calls = %d, want 1; calls: %q", got, claude)
	}
	if got := countLines(claude, "mcp add --scope user peer -- "+binary+" mcp"); got != 1 {
		t.Errorf("mcp add calls = %d, want 1 with %s; calls: %q", got, binary, claude)
	}

	mustContain(t, "stdout", stdout, "agent-peer installed")
	mustContain(t, "stdout", stdout, "MCP:     registered")
	mustContain(t, "stdout", stdout, "Reopen any Claude Code session")
	mustContain(t, "stdout", stdout, binary[:strings.LastIndex(binary, "/")]+" is not on PATH")

	// The script must run the installed binary, never one found on PATH.
	if calls := readLines(t, f.pathAgentLog); len(calls) != 0 {
		t.Errorf("install.sh called agent-peer from PATH: %q", calls)
	}
}

func TestInstallScriptUserInstallRejectsRoot(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_ID_UID": "0"}, "--user")
	if code != 1 {
		t.Fatalf("--user as root exited %d, want 1\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "must not run as root")
	mustContain(t, "stderr", stderr, "drop --user")

	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the binary was installed before the root check failed: %q", got)
	}
	if got := readLines(t, f.agentLog); len(got) != 0 {
		t.Errorf("the binary ran before the root check failed: %q", got)
	}
	if got := readLines(t, f.claudeLog); len(got) != 0 {
		t.Errorf("claude ran before the root check failed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the root check left files under $HOME: %v", err)
	}
}

func TestInstallScriptCustomPrefix(t *testing.T) {
	f := newFixture(t)
	prefix := filepath.Join(f.base, "custom", "bin")
	stdout, stderr, code := f.run(nil, "--prefix", prefix)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	binary := f.userBinary(prefix)
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("the installed binary is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local", "bin", "agent-peer")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the default prefix was used as well: %v", err)
	}
	claude := readLines(t, f.claudeLog)
	if got := countLines(claude, "mcp add --scope user peer -- "+binary+" mcp"); got != 1 {
		t.Errorf("mcp add calls = %d, want 1 with %s; calls: %q", got, binary, claude)
	}
	mustContain(t, "stdout", stdout, "binary:  "+binary)
}

func TestInstallScriptNoPathHintWhenPrefixIsOnPath(t *testing.T) {
	f := newFixture(t)
	prefix := f.pathUnderHome(t, ".local", "bin")
	path := f.fakes + string(os.PathListSeparator) + prefix + string(os.PathListSeparator) + systemPath
	stdout, stderr, code := f.run(map[string]string{"PATH": path})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "is not on PATH") {
		t.Errorf("output mentions PATH although %s is already on it:\n%s\n%s", prefix, stdout, stderr)
	}
}

func TestInstallScriptClaudeMissing(t *testing.T) {
	f := newFixture(t)
	// Without the fake bin directory there is no claude on PATH. The real
	// install still runs from the system directories.
	stdout, stderr, code := f.run(map[string]string{"PATH": systemPath})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	binary := f.pathUnderHome(t, ".local", "bin", "agent-peer")
	combined := stdout + stderr
	mustContain(t, "output", combined, "claude mcp add --scope user peer -- "+binary+" mcp")
	mustContain(t, "stdout", stdout, "MCP:     skipped (claude not found)")
	if calls := readLines(t, f.claudeLog); len(calls) != 0 {
		t.Errorf("claude was called although it is missing: %q", calls)
	}
}

func TestInstallScriptClaudeAlreadyRegistered(t *testing.T) {
	f := newFixture(t)
	binary := f.pathUnderHome(t, ".local", "bin", "agent-peer")
	writeRegistered(t, f.claudeState, binary)

	stdout, stderr, code := f.run(nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if got := countLines(readLines(t, f.claudeLog), "mcp add"); got != 0 {
		t.Errorf("mcp add calls = %d, want 0", got)
	}
	mustContain(t, "stdout", stdout, "already registered")
	if strings.Contains(stdout, "different command") {
		t.Errorf("stdout reports a different command for the same path:\n%s", stdout)
	}
}

func TestInstallScriptClaudeRegisteredElsewhere(t *testing.T) {
	f := newFixture(t)
	writeRegistered(t, f.claudeState, "/opt/other/agent-peer")

	stdout, stderr, code := f.run(nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if got := countLines(readLines(t, f.claudeLog), "mcp add"); got != 0 {
		t.Errorf("mcp add calls = %d, want 0; the existing registration must stay untouched", got)
	}
	mustContain(t, "stdout", stdout, "/opt/other/agent-peer")
	mustContain(t, "stdout", stdout, "leaving it unchanged")
	mustContain(t, "stdout", stdout, "claude mcp remove -s user peer")
}

func TestInstallScriptRegistersOnceAcrossRuns(t *testing.T) {
	f := newFixture(t)
	for run := 1; run <= 2; run++ {
		stdout, stderr, code := f.run(nil)
		if code != 0 {
			t.Fatalf("run %d exit = %d, want 0\nstderr:\n%s", run, code, stderr)
		}
		if run == 2 {
			mustContain(t, "stdout on the second run", stdout, "already registered")
		}
	}
	if got := countLines(readLines(t, f.claudeLog), "mcp add"); got != 1 {
		t.Errorf("mcp add calls across two runs = %d, want 1; calls: %q", got, readLines(t, f.claudeLog))
	}
}

func TestInstallScriptNoServiceNoMCP(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := f.run(nil, "--no-service", "--no-mcp")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	agent := readLines(t, f.agentLog)
	if got := countLines(agent, "service install"); got != 0 {
		t.Errorf("service install calls = %d, want 0; calls: %q", got, agent)
	}
	if got := countLines(agent, "status"); got != 0 {
		t.Errorf("status calls = %d, want 0; calls: %q", got, agent)
	}
	if calls := readLines(t, f.claudeLog); len(calls) != 0 {
		t.Errorf("claude was called with --no-mcp: %q", calls)
	}
	mustContain(t, "stdout", stdout, "service: skipped (--no-service)")
	mustContain(t, "stdout", stdout, "MCP:     skipped (--no-mcp)")
}

func TestInstallScriptRejectsUnexpectedVersion(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_AGENT_VERSION": "not agent-peer"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	mustContain(t, "stderr", stderr, "unexpected output from")
	if got := countLines(readLines(t, f.agentLog), "service install"); got != 0 {
		t.Errorf("service install calls = %d, want 0 after a version mismatch", got)
	}
}

func TestInstallScriptRequiresTheArchiveBinary(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.archive, "agent-peer")); err != nil {
		t.Fatalf("removing the archive binary: %v", err)
	}
	_, stderr, code := f.run(nil)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	mustContain(t, "stderr", stderr, "no agent-peer binary")
}

func TestInstallScriptReportsInstallFailure(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_INSTALL_COPY": "fail"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	mustContain(t, "stderr", stderr, "installing the binary to")
}

func TestInstallScriptReportsServiceFailure(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_AGENT_SERVICE_EXIT": "1"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	mustContain(t, "stderr", stderr, "installing the user-level service")
}

func TestInstallScriptAcceptsDegradedService(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := f.run(map[string]string{"FAKE_AGENT_SERVICE_EXIT": "3"})
	if code != 3 {
		t.Fatalf("exit = %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// service install printed the summary; the script kept going through the
	// MCP registration and its own summary.
	mustContain(t, "stdout", stdout, "installed (user-level, starts at login)")
	mustContain(t, "stdout", stdout, "agent-peer installed")
	mustContain(t, "stdout", stdout, "MCP:     registered")
	mustContain(t, "stderr", stderr, "Broker: degraded (local only)")
	mustContain(t, "stderr", stderr, "Fix:")

	agent := readLines(t, f.agentLog)
	if got := countLines(agent, "service install"); got != 1 {
		t.Errorf("service install calls = %d, want 1; calls: %q", got, agent)
	}
	claude := readLines(t, f.claudeLog)
	if got := countLines(claude, "mcp add --scope user peer --"); got != 1 {
		t.Errorf("mcp add calls = %d, want 1; calls: %q", got, claude)
	}
}

func TestInstallScriptToleratesStatusFailure(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := f.run(map[string]string{"FAKE_AGENT_STATUS": "fail"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stdout", stdout, "status:  not available yet")
	mustContain(t, "stdout", stdout, "agent-peer installed")
}

// hostSystemDefinitionPath is the existence check the script makes for a
// system-level service on this platform.
func hostSystemDefinitionPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist"
	}
	return "/etc/systemd/system/agent-peer.service"
}

const (
	levelInstallPrompt = "Install system-wide (all accounts)? [y/N]"
	levelUpgradePrompt = "Upgrade the system-wide installation? [Y/n]"
)

func TestInstallScriptSystemReexecutesThroughSudo(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "1000",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
	}
	stdout, stderr, code := f.run(env, "--system", "--prefix", prefix,
		"--no-service", "--no-mcp", "--admin-group", "ops", "--join-admin-group")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	want := "sudo sh " + filepath.Join(f.resolvedArchive(t), "install.sh") +
		" --system --prefix " + prefix +
		" --no-service --no-mcp --admin-group ops --join-admin-group"
	calls := readLines(t, f.sudoLog)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("sudo calls = %q, want exactly %q", calls, want)
	}
	if _, err := os.Stat(f.installedBinary(t, prefix)); err != nil {
		t.Fatalf("the binary is missing after the sudo re-exec: %v", err)
	}
	if got := countLines(readLines(t, f.installLog), "-o root -g 0 -m 0755"); got != 1 {
		t.Errorf("root-owned install calls = %d, want 1; calls: %q", got, readLines(t, f.installLog))
	}
}

func TestInstallScriptSystemWithoutSudo(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "1000",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
		"PATH":              f.pathWithoutSudo(t),
	}
	_, stderr, code := f.run(env, "--system", "--prefix", prefix, "--no-service", "--no-mcp")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "needs sudo")
	mustContain(t, "stderr", stderr, "--user")
	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the binary was installed without sudo: %q", got)
	}
}

func TestInstallScriptReportsSudoFailure(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "1000",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
		"FAKE_SUDO_EXIT":    "1",
	}
	_, stderr, code := f.run(env, "--system", "--prefix", prefix, "--no-service", "--no-mcp")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "the system-level installation did not complete")
	mustContain(t, "stderr", stderr, "sudo exited with status 1")
	// The sudo run has already printed its own error; the outer message must
	// not tell the caller to retry the same system-level command.
	mustContain(t, "stderr", stderr, "See the error above")
	mustContain(t, "stderr", stderr, "--user")
	mustNotContain(t, "stderr", stderr, "Rerun the installation command with sudo")
	mustNotContain(t, "stderr", stderr, "rerun with sudo")
	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the binary was installed although sudo failed: %q", got)
	}
}

// TestInstallScriptPassesThroughSudoDegradedExit checks that the degraded
// service status reaches the caller unchanged and is not reported as a failed
// installation.
func TestInstallScriptPassesThroughSudoDegradedExit(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "1000",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
		"FAKE_SUDO_EXIT":    "3",
	}
	_, stderr, code := f.run(env, "--system", "--prefix", prefix, "--no-service", "--no-mcp")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 from the degraded service\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "did not complete") {
		t.Errorf("a degraded service was reported as a failed install:\n%s", stderr)
	}
	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the outer install installed a binary although the sudo run reported a degraded service: %q", got)
	}
}

// pathWithoutSudo builds a search path with every fake command except sudo,
// plus the real tools the script needs outside the fake set. The host's real
// sudo is then invisible, so a test can check the missing-sudo error.
func (f *fixture) pathWithoutSudo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(f.base, "nosudobin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	entries, err := os.ReadDir(f.fakes)
	if err != nil {
		t.Fatalf("reading %s: %v", f.fakes, err)
	}
	for _, entry := range entries {
		if entry.Name() == "sudo" {
			continue
		}
		if err := os.Symlink(filepath.Join(f.fakes, entry.Name()), filepath.Join(dir, entry.Name())); err != nil {
			t.Fatalf("linking %s: %v", entry.Name(), err)
		}
	}
	for _, name := range []string{"grep", "dirname"} {
		if err := os.Symlink(mustLookPath(t, name), filepath.Join(dir, name)); err != nil {
			t.Fatalf("linking the real %s: %v", name, err)
		}
	}
	return dir
}

// skipWithoutTTYForwarding skips the prompt tests on the platforms whose
// script(1) does not forward a piped answer to the terminal.
func skipWithoutTTYForwarding(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("the macOS script(1) does not forward a piped answer to /dev/tty; " +
			"the no-terminal, flag and Darwin branch tests cover the level choice")
	}
}

func TestInstallScriptLevelPromptInstallsSystemWide(t *testing.T) {
	skipWithoutTTYForwarding(t)
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"y", "y\n"},
		{"yes", "yes\n"},
		{"mixed-case yes", "YeS\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := map[string]string{
				"FAKE_ID_UID":       "1000",
				"FAKE_STAT_OWNER":   "0",
				"FAKE_STAT_MODE":    "755",
				"FAKE_INSTALL_COPY": "1",
			}
			stdout, stderr, tty, code := f.runWithTTY(t, tc.answer, env,
				"--prefix", prefix, "--no-service", "--no-mcp")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
			}
			mustContain(t, "terminal", tty, levelInstallPrompt)
			if calls := readLines(t, f.sudoLog); len(calls) != 1 || !strings.Contains(calls[0], " --system ") {
				t.Fatalf("sudo calls = %q, want one system-level re-exec", calls)
			}
			if _, err := os.Stat(f.installedBinary(t, prefix)); err != nil {
				t.Fatalf("the binary is missing after choosing the system level: %v", err)
			}
		})
	}
}

func TestInstallScriptLevelPromptInstallsUserLevel(t *testing.T) {
	skipWithoutTTYForwarding(t)
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"enter", "\n"},
		{"n", "n\n"},
		{"other answer", "maybe\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := filepath.Join(f.base, "userbin")
			env := map[string]string{"FAKE_ID_UID": "1000"}
			stdout, stderr, tty, code := f.runWithTTY(t, tc.answer, env,
				"--prefix", prefix, "--no-service", "--no-mcp")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
			}
			mustContain(t, "terminal", tty, levelInstallPrompt)
			if calls := readLines(t, f.sudoLog); len(calls) != 0 {
				t.Errorf("sudo calls = %q, want none for a user-level install", calls)
			}
			if _, err := os.Stat(f.userBinary(prefix)); err != nil {
				t.Fatalf("the user-level binary is missing: %v", err)
			}
		})
	}
}

func TestInstallScriptLevelPromptUpgrades(t *testing.T) {
	skipWithoutTTYForwarding(t)
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"enter", "\n"},
		{"y", "y\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := map[string]string{
				"FAKE_ID_UID":            "1000",
				"FAKE_STAT_OWNER":        "0",
				"FAKE_STAT_MODE":         "755",
				"FAKE_INSTALL_COPY":      "1",
				"FAKE_STAT_PRESENT_PATH": hostSystemDefinitionPath(),
			}
			stdout, stderr, tty, code := f.runWithTTY(t, tc.answer, env,
				"--prefix", prefix, "--no-service", "--no-mcp")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
			}
			mustContain(t, "terminal", tty, levelUpgradePrompt)
			mustNotContain(t, "terminal", tty, levelInstallPrompt)
			if calls := readLines(t, f.sudoLog); len(calls) != 1 || !strings.Contains(calls[0], " --system ") {
				t.Fatalf("sudo calls = %q, want one system-level re-exec", calls)
			}
			if _, err := os.Stat(f.installedBinary(t, prefix)); err != nil {
				t.Fatalf("the binary is missing after the upgrade: %v", err)
			}
		})
	}
}

func TestInstallScriptLevelPromptDeclinesUpgrade(t *testing.T) {
	skipWithoutTTYForwarding(t)
	f := newFixture(t)
	prefix := filepath.Join(f.base, "userbin")
	env := map[string]string{
		"FAKE_ID_UID":            "1000",
		"FAKE_STAT_PRESENT_PATH": hostSystemDefinitionPath(),
	}
	stdout, stderr, tty, code := f.runWithTTY(t, "n\n", env,
		"--prefix", prefix, "--no-service", "--no-mcp")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
	}
	mustContain(t, "terminal", tty, levelUpgradePrompt)
	mustContain(t, "stdout", stdout, "--user")
	for what, path := range map[string]string{"sudo log": f.sudoLog, "install log": f.installLog, "agent log": f.agentLog, "claude log": f.claudeLog} {
		if calls := readLines(t, path); len(calls) != 0 {
			t.Errorf("%s = %q, want no changes when the upgrade is declined", what, calls)
		}
	}
	if _, err := os.Stat(prefix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the declined upgrade created %s: %v", prefix, err)
	}
}

func TestInstallScriptRootDoesNotAskForLevel(t *testing.T) {
	skipWithoutTTYForwarding(t)
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "0",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
	}
	stdout, stderr, tty, code := f.runWithTTY(t, "", env,
		"--prefix", prefix, "--no-service", "--no-mcp")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
	}
	mustNotContain(t, "terminal", tty, levelInstallPrompt)
	mustNotContain(t, "terminal", tty, levelUpgradePrompt)
	if _, err := os.Stat(f.installedBinary(t, prefix)); err != nil {
		t.Fatalf("the system-level binary is missing: %v", err)
	}
}

func TestInstallScriptExplicitLevelSkipsPrompt(t *testing.T) {
	skipWithoutTTYForwarding(t)
	t.Run("--user", func(t *testing.T) {
		f := newFixture(t)
		prefix := filepath.Join(f.base, "userbin")
		_, stderr, tty, code := f.runWithTTY(t, "", map[string]string{"FAKE_ID_UID": "1000"},
			"--user", "--prefix", prefix, "--no-service", "--no-mcp")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr:\n%s\nterminal:\n%s", code, stderr, tty)
		}
		mustNotContain(t, "terminal", tty, levelInstallPrompt)
		mustNotContain(t, "terminal", tty, levelUpgradePrompt)
		if _, err := os.Stat(f.userBinary(prefix)); err != nil {
			t.Fatalf("the user-level binary is missing: %v", err)
		}
	})

	t.Run("--system", func(t *testing.T) {
		f := newFixture(t)
		prefix := f.systemPrefix(t)
		env := map[string]string{
			"FAKE_ID_UID":       "1000",
			"FAKE_STAT_OWNER":   "0",
			"FAKE_STAT_MODE":    "755",
			"FAKE_INSTALL_COPY": "1",
		}
		_, stderr, tty, code := f.runWithTTY(t, "", env,
			"--system", "--prefix", prefix, "--no-service", "--no-mcp")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr:\n%s\nterminal:\n%s", code, stderr, tty)
		}
		mustNotContain(t, "terminal", tty, levelInstallPrompt)
		mustNotContain(t, "terminal", tty, levelUpgradePrompt)
		if calls := readLines(t, f.sudoLog); len(calls) != 1 {
			t.Fatalf("sudo calls = %q, want one system-level re-exec", calls)
		}
	})
}

func TestInstallScriptBothLevelsIsAUsageError(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(nil, "--system", "--user")
	if code != 2 {
		t.Fatalf("--system --user exit = %d, want 2\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "--system and --user cannot be used together")
	for what, path := range map[string]string{"install log": f.installLog, "agent log": f.agentLog, "sudo log": f.sudoLog} {
		if calls := readLines(t, path); len(calls) != 0 {
			t.Errorf("%s = %q, want no changes", what, calls)
		}
	}
}

func TestInstallScriptWithoutTerminalInstallsUserLevel(t *testing.T) {
	f := newFixture(t)
	prefix := filepath.Join(f.base, "userbin")
	// stdin says yes, but without a terminal the answer must not be read, so
	// the install stays user-level.
	stdout, stderr, code := f.runDetached("y\n", map[string]string{"FAKE_ID_UID": "1000"},
		"--prefix", prefix, "--no-service", "--no-mcp")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	mustNotContain(t, "stdout", stdout, levelInstallPrompt)
	mustNotContain(t, "stderr", stderr, "/dev/tty")
	if calls := readLines(t, f.sudoLog); len(calls) != 0 {
		t.Errorf("sudo calls = %q, want none without a terminal", calls)
	}
	if _, err := os.Stat(f.userBinary(prefix)); err != nil {
		t.Fatalf("the user-level binary is missing: %v", err)
	}
}

func TestInstallScriptAdminGroupAcceptedAfterPrompt(t *testing.T) {
	skipWithoutTTYForwarding(t)
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := map[string]string{
		"FAKE_ID_UID":       "1000",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
	}
	_, stderr, tty, code := f.runWithTTY(t, "y\n", env,
		"--admin-group", "operators", "--prefix", prefix, "--no-service", "--no-mcp")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s\nterminal:\n%s", code, stderr, tty)
	}
	if calls := readLines(t, f.sudoLog); len(calls) != 1 || !strings.Contains(calls[0], "--admin-group operators") {
		t.Fatalf("sudo calls = %q, want the admin group to reach the system-level re-exec", calls)
	}
	mustNotContain(t, "stderr", stderr, "only valid with --system")
}

func TestInstallScriptAdminGroupRejectedForPromptedUserLevel(t *testing.T) {
	skipWithoutTTYForwarding(t)
	f := newFixture(t)
	prefix := filepath.Join(f.base, "userbin")
	_, stderr, tty, code := f.runWithTTY(t, "\n", map[string]string{"FAKE_ID_UID": "1000"},
		"--admin-group", "operators", "--prefix", prefix, "--no-service", "--no-mcp")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr:\n%s\nterminal:\n%s", code, stderr, tty)
	}
	mustContain(t, "terminal", tty, levelInstallPrompt)
	mustContain(t, "stderr", stderr, "--admin-group is only valid with --system")
	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the binary was installed before the admin group was rejected: %q", got)
	}
}

func TestInstallScriptSystemRejectsUnsafeDirectory(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		mode  string
	}{
		{"not owned by root", "1000", "755"},
		{"group writable", "0", "775"},
		{"other writable", "0", "757"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := filepath.Join(f.base, "sysbin")
			if err := os.MkdirAll(prefix, 0o755); err != nil {
				t.Fatalf("creating %s: %v", prefix, err)
			}
			env := map[string]string{
				"FAKE_ID_UID":       "0",
				"FAKE_STAT_OWNER":   tc.owner,
				"FAKE_STAT_MODE":    tc.mode,
				"FAKE_INSTALL_COPY": "1",
			}
			_, stderr, code := f.run(env, "--system", "--no-service", "--no-mcp", "--prefix", prefix)
			if code != 1 {
				t.Fatalf("exit = %d, want 1\nstderr:\n%s", code, stderr)
			}
			mustContain(t, "stderr", stderr, "--prefix")
			if got := readLines(t, f.installLog); len(got) != 0 {
				t.Errorf("the binary was installed into an unsafe directory: %q", got)
			}
		})
	}
}

func TestInstallScriptSystemRejectsUnsafeAncestor(t *testing.T) {
	cases := []struct {
		name   string
		owner  string
		mode   string
		detail string
	}{
		{"group writable ancestor", "0", "775", "writable by group or others"},
		{"ancestor not owned by root", "1000", "755", "not owned by root"},
		{"ancestor not searchable by others", "0", "750", "not searchable by others"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := filepath.Join(f.base, "sysbin")
			if err := os.MkdirAll(prefix, 0o755); err != nil {
				t.Fatalf("creating %s: %v", prefix, err)
			}
			// Only this ancestor answers with the unsafe values; the install
			// directory itself stays root-owned and safe.
			ancestor, err := filepath.EvalSymlinks(f.base)
			if err != nil {
				t.Fatalf("resolving %s: %v", f.base, err)
			}
			env := map[string]string{
				"FAKE_ID_UID":            "0",
				"FAKE_STAT_UNSAFE_PATH":  ancestor,
				"FAKE_STAT_UNSAFE_OWNER": tc.owner,
				"FAKE_STAT_UNSAFE_MODE":  tc.mode,
				"FAKE_INSTALL_COPY":      "1",
			}
			_, stderr, code := f.run(env, "--system", "--no-service", "--no-mcp", "--prefix", prefix)
			if code != 1 {
				t.Fatalf("exit = %d, want 1\nstderr:\n%s", code, stderr)
			}
			mustContain(t, "stderr", stderr, tc.detail)
			mustContain(t, "stderr", stderr, ancestor)
			if got := readLines(t, f.installLog); len(got) != 0 {
				t.Errorf("the binary was installed before the ancestor check failed: %q", got)
			}
		})
	}
}

func TestInstallScriptSystemInstallsIntoRootOwnedDirectory(t *testing.T) {
	f := newFixture(t)
	prefix := filepath.Join(f.base, "sysbin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatalf("creating %s: %v", prefix, err)
	}
	env := map[string]string{
		"FAKE_ID_UID":       "0",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
	}
	stdout, stderr, code := f.run(env, "--system", "--no-service", "--no-mcp", "--prefix", prefix)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	installs := readLines(t, f.installLog)
	if got := countLines(installs, "-o root -g 0 -m 0755"); got != 1 {
		t.Errorf("root-owned install calls = %d, want 1; calls: %q", got, installs)
	}
	binary := f.installedBinary(t, prefix)
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("the installed binary is missing: %v", err)
	}
	if got := countLines(readLines(t, f.agentLog), "--version"); got != 1 {
		t.Errorf("--version calls = %d, want 1", got)
	}
	mustContain(t, "stdout", stdout, "service: skipped (--no-service)")
	mustContain(t, "stdout", stdout, "MCP:     skipped (--no-mcp)")
}

func TestInstallScriptSystemChecksDirectoriesWithBSDStat(t *testing.T) {
	f := newFixture(t)
	prefix := filepath.Join(f.base, "sysbin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatalf("creating %s: %v", prefix, err)
	}
	env := map[string]string{
		"FAKE_ID_UID":          "0",
		"FAKE_STAT_OWNER":      "0",
		"FAKE_STAT_MODE":       "755",
		"FAKE_STAT_REJECT_GNU": "1",
		"FAKE_INSTALL_COPY":    "1",
	}
	_, stderr, code := f.run(env, "--system", "--no-service", "--no-mcp", "--prefix", prefix)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; the BSD stat fallback did not work\nstderr:\n%s", code, stderr)
	}
}

func TestInstallScriptSystemPrintsManualMCPCommand(t *testing.T) {
	cases := []struct {
		name     string
		sudoUser string // empty means the variable is not set
	}{
		{"no sudo user", ""},
		{"sudo user is root", "root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := filepath.Join(f.base, "sysbin")
			if err := os.MkdirAll(prefix, 0o755); err != nil {
				t.Fatalf("creating %s: %v", prefix, err)
			}
			env := map[string]string{
				"FAKE_ID_UID":       "0",
				"FAKE_STAT_OWNER":   "0",
				"FAKE_STAT_MODE":    "755",
				"FAKE_INSTALL_COPY": "1",
			}
			if tc.sudoUser != "" {
				env["SUDO_USER"] = tc.sudoUser
			}
			stdout, stderr, code := f.run(env, "--system", "--no-service", "--prefix", prefix)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			binary := f.installedBinary(t, prefix)
			combined := stdout + stderr
			mustContain(t, "output", combined, "claude mcp add --scope user peer -- "+binary+" mcp")
			mustContain(t, "stdout", stdout, "MCP:     skipped (no account to register for)")
			if calls := readLines(t, f.claudeLog); len(calls) != 0 {
				t.Errorf("claude was called without a user to register for: %q", calls)
			}
		})
	}
}

func TestInstallScriptSystemRegistersForSudoUser(t *testing.T) {
	f := newFixture(t)
	prefix := filepath.Join(f.base, "sysbin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatalf("creating %s: %v", prefix, err)
	}
	env := map[string]string{
		"FAKE_ID_UID":       "0",
		"FAKE_STAT_OWNER":   "0",
		"FAKE_STAT_MODE":    "755",
		"FAKE_INSTALL_COPY": "1",
		"SUDO_USER":         "alice",
	}
	binary := f.installedBinary(t, prefix)
	for run := 1; run <= 2; run++ {
		stdout, stderr, code := f.run(env, "--system", "--no-service", "--prefix", prefix)
		if code != 0 {
			t.Fatalf("run %d exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", run, code, stdout, stderr)
		}
		if run == 1 {
			mustContain(t, "stdout", stdout, "MCP:     registered")
		} else {
			mustContain(t, "stdout", stdout, "MCP:     already registered")
		}
	}
	if got := countLines(readLines(t, f.claudeLog), "mcp add --scope user peer -- "+binary+" mcp"); got != 1 {
		t.Errorf("mcp add calls across two system runs = %d, want 1; calls: %q", got, readLines(t, f.claudeLog))
	}
}

// adminGroupPrompt is the question the script asks on the controlling terminal.
func adminGroupPrompt(user, group string) string {
	return "Add " + user + " to the " + group + " group so " + user + " can manage the Broker without sudo? [y/N]"
}

// adminGroupHint is the closing notice after the account joined the group.
func adminGroupHint(user, group string) string {
	return user + " was added to " + group + "; log in again or run `newgrp " + group + "` before managing the Broker without sudo."
}

// adminGroupJoinCommand is the command the script prints when the account is
// left out of the group on Linux.
func adminGroupJoinCommand(user, group string) string {
	return "install.sh: to add " + user + " to the " + group + " group, run: sudo usermod -aG " + group + " " + user
}

// darwinAdminGroupJoinCommand is the same command on macOS.
func darwinAdminGroupJoinCommand(user, group string) string {
	return "install.sh: to add " + user + " to the " + group + " group, run: sudo dseditgroup -o edit -a " + user + " -t user " + group
}

// hostAdminGroupJoinCommand is the join command the script prints on the host
// platform. The fake uname reports the host OS unless a test forces Darwin.
func hostAdminGroupJoinCommand(user, group string) string {
	if runtime.GOOS == "darwin" {
		return darwinAdminGroupJoinCommand(user, group)
	}
	return adminGroupJoinCommand(user, group)
}

// hostAdminGroupJoinCall is the group command the script runs on the host
// platform.
func hostAdminGroupJoinCall(user, group string) string {
	if runtime.GOOS == "darwin" {
		return "dseditgroup -o edit -a " + user + " -t user " + group
	}
	return "usermod -aG " + group + " " + user
}

func TestInstallScriptSystemPromptJoinsAdminGroup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the macOS script(1) does not forward a piped answer to /dev/tty; " +
			"the join flag and no-terminal tests cover the Darwin branch")
	}
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"y", "y\n"},
		{"Y", "Y\n"},
		{"yes", "yes\n"},
		{"mixed-case yes", "YeS\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := f.systemInstallEnv("alice")
			stdout, stderr, tty, code := f.runWithTTY(t, tc.answer, env, systemInstallArgs(prefix)...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
			}
			if calls := readLines(t, f.groupLog); len(calls) != 1 || calls[0] != "usermod -aG agent-peer alice" {
				t.Fatalf("group command calls = %q, want one usermod call", calls)
			}
			mustContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
			mustContain(t, "terminal", tty, adminGroupPrompt("alice", "agent-peer"))
			mustNotContain(t, "stdout", stdout, adminGroupJoinCommand("alice", "agent-peer"))
		})
	}
}

func TestInstallScriptSystemPromptDeclinesAdminGroup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the macOS script(1) does not forward a piped answer to /dev/tty; " +
			"the no-terminal and Darwin decline tests cover the refusal")
	}
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"enter", "\n"},
		{"n", "n\n"},
		{"other answer", "maybe\n"},
		{"a word starting with y", "yeah\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := f.systemInstallEnv("alice")
			stdout, stderr, tty, code := f.runWithTTY(t, tc.answer, env, systemInstallArgs(prefix)...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
			}
			if calls := readLines(t, f.groupLog); len(calls) != 0 {
				t.Fatalf("group command calls = %q, want none", calls)
			}
			mustContain(t, "stdout", stdout, adminGroupJoinCommand("alice", "agent-peer"))
			mustNotContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
			mustContain(t, "terminal", tty, adminGroupPrompt("alice", "agent-peer"))
		})
	}
}

func TestInstallScriptSystemPromptWithoutTerminal(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	// stdin says yes, but without a terminal the answer must not come from
	// stdin, so the account stays out of the group.
	stdout, stderr, code := f.runDetached("y\n", env, systemInstallArgs(prefix)...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if calls := readLines(t, f.groupLog); len(calls) != 0 {
		t.Fatalf("group command calls = %q, want none", calls)
	}
	mustContain(t, "stdout", stdout, hostAdminGroupJoinCommand("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, adminGroupPrompt("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
	mustNotContain(t, "stderr", stderr, "/dev/tty")
}

func TestInstallScriptSystemJoinAdminGroupWithoutPrompt(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	// stdin says no and there is no terminal: --join-admin-group must join
	// without reading either.
	stdout, stderr, code := f.runDetached("n\n", env, systemInstallArgs(prefix, "--join-admin-group")...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if calls := readLines(t, f.groupLog); len(calls) != 1 || calls[0] != hostAdminGroupJoinCall("alice", "agent-peer") {
		t.Fatalf("group command calls = %q, want %q", calls, hostAdminGroupJoinCall("alice", "agent-peer"))
	}
	mustContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, hostAdminGroupJoinCommand("alice", "agent-peer"))
}

func TestInstallScriptSystemNoAdminGroup(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	// A terminal answers yes, but --no-admin-group must suppress the prompt and
	// the membership change.
	stdout, stderr, tty, code := f.runWithTTY(t, "y\n", env, systemInstallArgs(prefix, "--no-admin-group")...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
	}
	if calls := readLines(t, f.groupLog); len(calls) != 0 {
		t.Fatalf("group command calls = %q, want none", calls)
	}
	mustNotContain(t, "terminal", tty, adminGroupPrompt("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, hostAdminGroupJoinCommand("alice", "agent-peer"))
}

func TestInstallScriptSystemAdminGroupFlagsConflict(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	_, stderr, code := f.run(env, systemInstallArgs(prefix, "--join-admin-group", "--no-admin-group")...)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "--join-admin-group and --no-admin-group cannot be used together")
	mustContain(t, "stderr", stderr, "usage:")
	if got := readLines(t, f.installLog); len(got) != 0 {
		t.Errorf("the conflict was rejected after installing the binary: %q", got)
	}
	if got := readLines(t, f.agentLog); len(got) != 0 {
		t.Errorf("the conflict was rejected after running the binary: %q", got)
	}
}

func TestInstallScriptSystemRejectsInvalidAdminGroupName(t *testing.T) {
	for _, name := range []string{"-bad", "Agent", "1ops", "a b", "a.b"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := f.systemInstallEnv("alice")
			_, stderr, code := f.run(env, systemInstallArgs(prefix, "--admin-group="+name)...)
			if code != 2 {
				t.Fatalf("invalid --admin-group %q: exit %d, want 2\nstderr:\n%s", name, code, stderr)
			}
			mustContain(t, "stderr", stderr, "invalid --admin-group name")
			if got := readLines(t, f.installLog); len(got) != 0 {
				t.Errorf("the invalid name was installed after validation: %q", got)
			}
		})
	}
}

func TestInstallScriptSystemAcceptsValidAdminGroupName(t *testing.T) {
	for _, name := range []string{"a", "_ops"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := f.systemInstallEnv("alice")
			stdout, stderr, code := f.runDetached("", env,
				systemInstallArgs(prefix, "--admin-group="+name, "--join-admin-group")...)
			if code != 0 {
				t.Fatalf("valid --admin-group %q: exit %d\nstdout:\n%s\nstderr:\n%s", name, code, stdout, stderr)
			}
			wantCall := "service install --system --admin-group " + name
			if got := countLines(readLines(t, f.agentLog), wantCall); got != 1 {
				t.Errorf("service install calls with %q = %d, want 1; calls: %q", wantCall, got, readLines(t, f.agentLog))
			}
		})
	}
}

func TestInstallScriptSystemSkipsAdminGroupWhenAlreadyMember(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	env["FAKE_ID_GROUPS"] = "alice staff agent-peer"
	stdout, stderr, tty, code := f.runWithTTY(t, "y\n", env, systemInstallArgs(prefix)...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s\nterminal:\n%s", code, stdout, stderr, tty)
	}
	if calls := readLines(t, f.groupLog); len(calls) != 0 {
		t.Fatalf("group command calls = %q, want none for an existing member", calls)
	}
	mustNotContain(t, "terminal", tty, adminGroupPrompt("alice", "agent-peer"))
	mustNotContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
}

func TestInstallScriptSystemSkipsAdminGroupWithoutSudoUser(t *testing.T) {
	for _, tc := range []struct {
		name string
		user string
	}{
		{"no sudo user", ""},
		{"sudo user is root", "root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			prefix := f.systemPrefix(t)
			env := f.systemInstallEnv(tc.user)
			stdout, stderr, code := f.runDetached("y\n", env, systemInstallArgs(prefix)...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if calls := readLines(t, f.groupLog); len(calls) != 0 {
				t.Fatalf("group command calls = %q, want none", calls)
			}
			mustNotContain(t, "stdout", stdout, "was added to")
			mustNotContain(t, "stdout", stdout, "to add ")
		})
	}
}

func TestInstallScriptSystemAdminGroupOnDarwin(t *testing.T) {
	t.Run("join", func(t *testing.T) {
		f := newFixture(t)
		prefix := f.systemPrefix(t)
		env := f.systemInstallEnv("alice")
		env["FAKE_UNAME"] = "Darwin"
		// --join-admin-group picks the platform command without a terminal, so
		// this checks the Darwin dseditgroup call without the pty harness.
		stdout, stderr, code := f.runDetached("", env, systemInstallArgs(prefix, "--join-admin-group")...)
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if calls := readLines(t, f.groupLog); len(calls) != 1 || calls[0] != "dseditgroup -o edit -a alice -t user agent-peer" {
			t.Fatalf("group command calls = %q, want one dseditgroup call", calls)
		}
		mustContain(t, "stdout", stdout, adminGroupHint("alice", "agent-peer"))
	})

	t.Run("decline", func(t *testing.T) {
		f := newFixture(t)
		prefix := f.systemPrefix(t)
		env := f.systemInstallEnv("alice")
		env["FAKE_UNAME"] = "Darwin"
		stdout, stderr, _, code := f.runWithTTY(t, "n\n", env, systemInstallArgs(prefix)...)
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if calls := readLines(t, f.groupLog); len(calls) != 0 {
			t.Fatalf("group command calls = %q, want none", calls)
		}
		mustContain(t, "stdout", stdout, darwinAdminGroupJoinCommand("alice", "agent-peer"))
		mustNotContain(t, "stdout", stdout, "sudo usermod")
	})
}

func TestInstallScriptSystemCustomAdminGroup(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	stdout, stderr, code := f.runDetached("n\n", env,
		systemInstallArgs(prefix, "--admin-group", "operators", "--join-admin-group")...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if calls := readLines(t, f.groupLog); len(calls) != 1 || calls[0] != hostAdminGroupJoinCall("alice", "operators") {
		t.Fatalf("group command calls = %q, want %q", calls, hostAdminGroupJoinCall("alice", "operators"))
	}
	if got := countLines(readLines(t, f.agentLog), "service install --system --admin-group operators"); got != 1 {
		t.Errorf("service install calls with --admin-group = %d, want 1; calls: %q", got, readLines(t, f.agentLog))
	}
	mustContain(t, "stdout", stdout, adminGroupHint("alice", "operators"))
}

func TestInstallScriptSystemAdminGroupFailure(t *testing.T) {
	f := newFixture(t)
	prefix := f.systemPrefix(t)
	env := f.systemInstallEnv("alice")
	env["FAKE_GROUP_EXIT"] = "1"
	_, stderr, code := f.runDetached("", env, systemInstallArgs(prefix, "--join-admin-group")...)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "adding alice to the agent-peer group")
}

func TestInstallScriptRemovesQuarantineOnDarwin(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_UNAME": "Darwin"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	want := "-d com.apple.quarantine " + filepath.Join(f.resolvedArchive(t), "agent-peer")
	mustContain(t, "xattr log", strings.Join(readLines(t, f.xattrLog), "\n"), want)

	order := readLines(t, f.orderLog)
	if len(order) < 2 {
		t.Fatalf("the order log = %q, want the xattr step before the install step", order)
	}
	if !strings.HasPrefix(order[0], "xattr ") {
		t.Errorf("first step = %q, want xattr", order[0])
	}
	if !strings.HasPrefix(order[1], "install ") {
		t.Errorf("second step = %q, want install", order[1])
	}
}

func TestInstallScriptIgnoresQuarantineFailure(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_UNAME": "Darwin", "FAKE_XATTR_EXIT": "1"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; a missing quarantine attribute is not an error\nstderr:\n%s", code, stderr)
	}
}

func TestInstallScriptHasNoNetworkOperations(t *testing.T) {
	data, err := os.ReadFile(installScriptPath)
	if err != nil {
		t.Fatalf("reading %s: %v", installScriptPath, err)
	}
	text := string(data)
	for _, tool := range []string{"curl", "wget"} {
		if strings.Contains(text, tool) {
			t.Errorf("install.sh mentions %q; the installer must not download anything", tool)
		}
	}
	if !strings.HasPrefix(text, "#!/bin/sh\n") {
		t.Errorf("install.sh does not start with a POSIX sh shebang")
	}
	if !strings.Contains(text, "set -eu") {
		t.Errorf("install.sh does not set -eu")
	}
}

const fakeAgentPeer = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_AGENT_LOG"
case $1 in
--version)
	printf '%s\n' "$FAKE_AGENT_VERSION"
	;;
service)
	if [ "${FAKE_AGENT_SERVICE_EXIT:-0}" != "0" ]; then
		if [ "$FAKE_AGENT_SERVICE_EXIT" = "3" ]; then
			printf 'agent-peer installed (user-level, starts at login)\n'
			printf '  binary:     unattested\n'
			printf 'Broker: degraded (local only): cannot bind 127.0.0.1:17443\n' >&2
			printf 'Fix: change broker.listen in the configuration\n' >&2
		fi
		exit "$FAKE_AGENT_SERVICE_EXIT"
	fi
	;;
status)
	if [ "$FAKE_AGENT_STATUS" = "fail" ]; then
		printf 'no broker is running\n' >&2
		exit 1
	fi
	printf 'broker: running\n'
	;;
esac
exit 0
`

const fakePathAgentPeer = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_PATH_AGENT_LOG"
exit 0
`

const fakeClaude = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_CLAUDE_LOG"
case "$1 $2" in
"mcp get")
	if [ -f "$FAKE_CLAUDE_STATE" ]; then
		cat "$FAKE_CLAUDE_STATE"
		exit 0
	fi
	printf 'No MCP server found with name: peer\n' >&2
	exit 1
	;;
"mcp add")
	{
		printf 'peer:\n'
		printf '  Scope: User config\n'
		printf '  Command: %s\n' "$7"
		printf '  Args: mcp\n'
	} > "$FAKE_CLAUDE_STATE"
	;;
esac
exit 0
`

const fakeStat = `#!/bin/sh
gnu=0
format=
path=
for arg in "$@"; do
	case $arg in
	-c) gnu=1 ;;
	-f) gnu=0 ;;
	-L) ;;
	%u|%a|%Lp) format=$arg ;;
	*) path=$arg ;;
	esac
done
if [ "$gnu" = 1 ] && [ "${FAKE_STAT_REJECT_GNU:-0}" = "1" ]; then
	exit 1
fi
# A bare path asks only whether the path exists. FAKE_STAT_PRESENT_PATH makes the
# platform's system service definition answer yes, so the level prompt can take
# its upgrade branch without touching the real system directories.
if [ -z "$format" ] && [ -n "${FAKE_STAT_PRESENT_PATH:-}" ] && [ "$path" = "$FAKE_STAT_PRESENT_PATH" ]; then
	exit 0
fi
owner=${FAKE_STAT_OWNER:-0}
mode=${FAKE_STAT_MODE:-755}
if [ -n "${FAKE_STAT_UNSAFE_PATH:-}" ] && [ "$path" = "$FAKE_STAT_UNSAFE_PATH" ]; then
	owner=${FAKE_STAT_UNSAFE_OWNER:-$owner}
	mode=${FAKE_STAT_UNSAFE_MODE:-$mode}
fi
case $format in
%u) printf '%s\n' "$owner" ;;
%a|%Lp) printf '%s\n' "$mode" ;;
*) exit 1 ;;
esac
`

const fakeID = `#!/bin/sh
case $1 in
-u)
	if [ -n "${FAKE_ID_UID:-}" ]; then
		printf '%s\n' "$FAKE_ID_UID"
	else
		exec @REAL_ID@ -u
	fi
	;;
-un)
	if [ -n "${FAKE_ID_UN:-}" ]; then
		printf '%s\n' "$FAKE_ID_UN"
	else
		exec @REAL_ID@ -un
	fi
	;;
-Gn|-nG)
	if [ -n "${FAKE_ID_GROUPS+x}" ]; then
		printf '%s\n' "$FAKE_ID_GROUPS"
	else
		exec @REAL_ID@ "$@"
	fi
	;;
*)
	exec @REAL_ID@ "$@"
	;;
esac
`

const fakeUname = `#!/bin/sh
if [ -n "${FAKE_UNAME:-}" ]; then
	printf '%s\n' "$FAKE_UNAME"
	exit 0
fi
exec @REAL_UNAME@ "$@"
`

const fakeXattr = `#!/bin/sh
printf 'xattr %s\n' "$*" >> "$FAKE_ORDER_LOG"
printf '%s\n' "$*" >> "$FAKE_XATTR_LOG"
exit "${FAKE_XATTR_EXIT:-0}"
`

const fakeSudo = `#!/bin/sh
printf 'sudo %s\n' "$*" >> "$FAKE_SUDO_LOG"
if [ "${FAKE_SUDO_EXIT:-0}" != "0" ]; then
	exit "$FAKE_SUDO_EXIT"
fi
# Drop the options the installer passes, then run the command. A login shell
# command (-lc) is run with sh -c so the test keeps its own PATH; the positional
# arguments still reach the command the same way. A plain sudo call is the
# system-level re-exec: it runs the rest of the installation as root, so the
# fake id reports uid 0 and SUDO_USER names the invoking account.
privileged=1
while [ "$#" -gt 0 ]; do
	case $1 in
	-u) privileged=0; shift 2 ;;
	-H) shift ;;
	*) break ;;
	esac
done
if [ "$2" = "-lc" ]; then
	shift 2
	exec sh -c "$@"
fi
if [ "$privileged" = 1 ]; then
	FAKE_ID_UID=0
	SUDO_USER=${FAKE_SUDO_USER:-alice}
	export FAKE_ID_UID SUDO_USER
fi
exec "$@"
`

const fakeUsermod = `#!/bin/sh
printf 'usermod %s\n' "$*" >> "$FAKE_GROUP_LOG"
exit "${FAKE_GROUP_EXIT:-0}"
`

const fakeDseditgroup = `#!/bin/sh
printf 'dseditgroup %s\n' "$*" >> "$FAKE_GROUP_LOG"
exit "${FAKE_GROUP_EXIT:-0}"
`

const fakeInstall = `#!/bin/sh
printf 'install %s\n' "$*" >> "$FAKE_ORDER_LOG"
case ${FAKE_INSTALL_COPY:-0} in
1)
	printf '%s\n' "$*" >> "$FAKE_INSTALL_LOG"
	src=
	dst=
	for arg in "$@"; do
		src=$dst
		dst=$arg
	done
	cp "$src" "$dst"
	exit $?
	;;
fail)
	exit 1
	;;
esac
exec @REAL_INSTALL@ "$@"
`

// writeExecutable writes a file with the executable bit set.
func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeRegistered writes the output claude mcp get returns for a registered
// peer server whose command is the given path.
func writeRegistered(t *testing.T, path, command string) {
	t.Helper()
	content := "peer:\n  Scope: User config\n  Command: " + command + "\n  Args: mcp\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// mustLookPath resolves a real command before the fake bin directory is on
// PATH, so a fake can delegate to it.
func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("looking up %s: %v", name, err)
	}
	return path
}

// shellQuote returns a single-quoted shell word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// readLines returns the non-empty lines of a file, or nil when it does not
// exist.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// readFile returns the contents of a file the test expects the script to write.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// countLines counts the lines that start with prefix.
func countLines(lines []string, prefix string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func mustContain(t *testing.T, what, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", what, got, want)
	}
}

func mustNotContain(t *testing.T, what, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("%s = %q, want it not to contain %q", what, got, want)
	}
}
