package installscript

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
	return f
}

// run executes the copied install.sh with the fixture environment. overrides
// replace or add an environment variable; a nil map keeps the defaults.
func (f *fixture) run(overrides map[string]string, args ...string) (stdout, stderr string, code int) {
	f.t.Helper()
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

	cmd := exec.Command("sh", append([]string{f.script}, args...)...)
	cmd.Dir = f.archive
	cmd.Env = environ
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
		for _, want := range []string{"--system", "--prefix <dir>", "--no-service", "--no-mcp"} {
			mustContain(t, "help output", stdout, want)
		}
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
	_, stderr, code := f.run(map[string]string{"FAKE_ID_UID": "0"})
	if code != 1 {
		t.Fatalf("a user-level install as root exited %d, want 1\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "must not run as root")
	mustContain(t, "stderr", stderr, "sudo sh ./install.sh --system")

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

func TestInstallScriptToleratesStatusFailure(t *testing.T) {
	f := newFixture(t)
	stdout, stderr, code := f.run(map[string]string{"FAKE_AGENT_STATUS": "fail"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	mustContain(t, "stdout", stdout, "status:  not available yet")
	mustContain(t, "stdout", stdout, "agent-peer installed")
}

func TestInstallScriptSystemRequiresRoot(t *testing.T) {
	f := newFixture(t)
	_, stderr, code := f.run(map[string]string{"FAKE_ID_UID": "1000"}, "--system")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	mustContain(t, "stderr", stderr, "sudo sh ./install.sh --system")
	if got := readLines(t, f.agentLog); len(got) != 0 {
		t.Errorf("the binary ran before the root check failed: %q", got)
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
# Drop the options the installer passes, then run the command. A login shell
# command (-lc) is run with sh -c so the test keeps its own PATH; the positional
# arguments still reach the command the same way.
while [ "$#" -gt 0 ]; do
	case $1 in
	-u) shift 2 ;;
	-H) shift ;;
	*) break ;;
	esac
done
if [ "$2" = "-lc" ]; then
	shift 2
	exec sh -c "$@"
fi
exec "$@"
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
