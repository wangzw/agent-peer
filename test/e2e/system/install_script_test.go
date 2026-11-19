//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// installAccount is the ordinary account the install script runs as. It
	// exists only in the image's extrausers source, so the script, sudo and the
	// user manager reach it through NSS.
	installAccount = "diruser"
	// installUID and installHome are that account's id and home directory.
	installUID  = uint32(2100)
	installHome = "/home/diruser"

	// installUserPrefix and installSystemPrefix are the default binary paths at
	// the two scopes.
	installUserPrefix   = installHome + "/.local/bin/agent-peer"
	installSystemPrefix = "/usr/local/bin/agent-peer"

	// installArchiveDir is where the inner test assembles a release-shaped
	// directory from the staged archive files and the image's release binary.
	installArchiveDir = "/var/lib/agent-peer-install-archive"

	// installClaudePath is the fake claude the script resolves through PATH.
	installClaudePath = "/usr/local/bin/claude"

	// installStateDir holds the fake claude's invocation log and the marker
	// that answers claude mcp get peer. It is world-writable because the script
	// runs claude as the ordinary account in both scenarios.
	installStateDir = "/var/lib/agent-peer-install-state"

	// installSudoersPath grants the ordinary account the passwordless sudo the
	// system-level scenario needs. The name has no dot, which sudo requires of
	// a file it reads from /etc/sudoers.d.
	installSudoersPath = "/etc/sudoers.d/agent-peer-install-script"

	// installPath is the PATH the user-level scenario gives the script. The
	// image keeps the fake claude in /usr/local/bin, which the default account
	// PATH in syscontainer.Command omits.
	installPath = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

	// installServiceStartBound bounds the wait for the Broker that the freshly
	// installed service starts. service install returns after starting it, so
	// the wait only covers systemd dispatching the unit.
	installServiceStartBound = 10 * time.Second

	// installStagedDir is the directory under the test binary mount where the
	// host stages the files copied from the repository.
	installStagedDir = "install-script-e2e"

	// installExpectedVersion is what the image's agent-peer reports. The image
	// is built through harness.BuildBinary, which stamps -X main.version=e2e.
	installExpectedVersion = "agent-peer e2e"

	// installUserConfigDir, installSystemConfigDir and the two unit paths are
	// the state a refused install must leave untouched.
	installUserConfigDir   = installHome + "/.agent-peer"
	installSystemConfigDir = "/etc/agent-peer"
	installUserUnitPath    = installHome + "/.config/systemd/user/agent-peer.service"
	installSystemUnitPath  = "/etc/systemd/system/agent-peer.service"
)

// installArchiveReadme stands in for the user-facing README a release archive
// carries. The install script does not read it; its presence keeps the staged
// directory shaped like the archive.
const installArchiveReadme = "agent-peer release archive for the install-script end-to-end test.\n"

// installFakeClaudeScript is the stand-in for Claude Code's CLI. It appends one
// tab-separated line per call to the invocation log and answers claude mcp get
// peer from a marker file, so the second install sees the registration the
// first one made.
const installFakeClaudeScript = `#!/bin/sh
state=/var/lib/agent-peer-install-state
log="$state/invocations"
registered="$state/registered-peer"
mkdir -p "$state" 2>/dev/null || true
printf '%s\t%s\t%s\t%s\t%s\n' "$(id -u)" "${USER:-}" "${HOME:-}" "${SUDO_USER:-}" "$*" >> "$log" 2>/dev/null || true

if [ "$1" = "mcp" ] && [ "$2" = "get" ] && [ "$3" = "peer" ]; then
	if [ -f "$registered" ]; then
		printf 'peer:\n  Scope: User\n  Command: %s mcp\n' "$(cat "$registered")"
		exit 0
	fi
	printf 'No MCP server found with name: peer\n' >&2
	exit 1
fi

if [ "$1" = "mcp" ] && [ "$2" = "add" ]; then
	previous=
	path=
	for argument in "$@"; do
		if [ "$previous" = "--" ]; then
			path=$argument
		fi
		previous=$argument
	done
	if [ -n "$path" ]; then
		printf '%s\n' "$path" > "$registered"
	fi
	exit 0
fi

exit 0
`

// TestREQBrokerConfig041_UserInstallScript runs the release archive's install
// script as the ordinary account and checks that it installs the binary at the
// user-level default path, starts the user-level service and registers the MCP
// server once. It runs the script a second time and checks that the install
// succeeds again without adding the MCP server a second time.
func TestREQBrokerConfig041_UserInstallScript(t *testing.T) {
	if !syscontainer.InContainer() {
		stageInstallArchive(t)
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	installFakeClaude(t)
	archive := prepareInstallArchive(t)
	startInstallUserManager(t)

	t.Run("REQ-broker-config-041 user install", func(t *testing.T) {
		runInstallScriptAs(t, installAccount, installUserEnv(), archive, "sh", "./install.sh")
		assertUserInstall(t)
		assertInstallMCPAdd(t, 1, installUserPrefix, installUID, installHome)
		assertInstallMCPGet(t, 1, installUID, installHome)
	})

	t.Run("REQ-broker-config-041 second user install is idempotent", func(t *testing.T) {
		runInstallScriptAs(t, installAccount, installUserEnv(), archive, "sh", "./install.sh")
		assertUserInstall(t)
		assertInstallMCPAdd(t, 1, installUserPrefix, installUID, installHome)
		assertInstallMCPGet(t, 2, installUID, installHome)
	})
}

// TestREQBrokerConfig041_SystemInstallScript runs the install script with
// --system through sudo as the ordinary account and checks that the system
// service runs, that the binary belongs to root and that the account sudo ran
// as received the MCP registration once. It runs the script a second time and
// checks that it succeeds again without adding the MCP server a second time.
func TestREQBrokerConfig041_SystemInstallScript(t *testing.T) {
	if !syscontainer.InContainer() {
		stageInstallArchive(t)
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	installFakeClaude(t)
	archive := prepareInstallArchive(t)
	removePreinstalledSystemBinary(t)
	configureInstallSudo(t)

	t.Run("REQ-broker-config-041 root user-level install is refused", func(t *testing.T) {
		assertInstallScriptRefusesRoot(t, archive)
	})

	t.Run("REQ-broker-config-041 system install", func(t *testing.T) {
		runSystemInstallScript(t, archive)
		assertSystemInstall(t)
		assertInstallMCPAdd(t, 1, installSystemPrefix, installUID, installHome)
		assertInstallMCPGet(t, 1, installUID, installHome)
	})

	t.Run("REQ-broker-config-041 second system install is idempotent", func(t *testing.T) {
		runSystemInstallScript(t, archive)
		assertSystemInstall(t)
		assertInstallMCPAdd(t, 1, installSystemPrefix, installUID, installHome)
		assertInstallMCPGet(t, 2, installUID, installHome)
	})
}

// installClaudeCall is one invocation recorded by the fake claude.
type installClaudeCall struct {
	UID      uint32
	User     string
	Home     string
	SudoUser string
	Args     string
}

// stageInstallArchive copies the install script and the license from the
// repository into the directory mount the container reads. It runs on the host;
// the inner run assembles them with the image's release binary.
func stageInstallArchive(t *testing.T) {
	t.Helper()

	rel := stagedArchiveRel(t)
	for _, file := range []struct {
		name   string
		source string
		mode   os.FileMode
	}{
		{"install.sh", "scripts/install.sh", 0o755},
		{"LICENSE", "LICENSE", 0o644},
	} {
		data := readRepositoryFile(t, filepath.Join("..", "..", "..", file.source))
		syscontainer.StageArtifact(t, filepath.Join(rel, file.name), data, file.mode)
	}
	syscontainer.StageArtifact(t, filepath.Join(rel, "README.md"), []byte(installArchiveReadme), 0o644)
}

// readRepositoryFile reads a file at rel from the repository root and fails the
// test with the path when it cannot.
func readRepositoryFile(t *testing.T, rel string) []byte {
	t.Helper()

	path, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("resolving %s: %v", rel, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s for the release archive: %v", path, err)
	}
	return data
}

// stagedArchiveRel returns the per-test relative directory the host stages the
// archive files in.
func stagedArchiveRel(t *testing.T) string {
	t.Helper()

	var b strings.Builder
	for _, r := range strings.ToLower(t.Name()) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return filepath.Join(installStagedDir, b.String())
}

// installUserEnv returns the environment the user-level script and its service
// manager need. The account's user manager is running, so the runtime directory
// and the session bus path are set the way a login sets them.
func installUserEnv() []string {
	uid := strconv.FormatUint(uint64(installUID), 10)
	return []string{
		"PATH=" + installPath,
		"XDG_RUNTIME_DIR=/run/user/" + uid,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + uid + "/bus",
	}
}

// prepareInstallArchive builds a directory shaped like the release archive:
// the release binary, the staged install script, the license and a README. It
// returns the directory.
func prepareInstallArchive(t *testing.T) string {
	t.Helper()

	if err := os.RemoveAll(installArchiveDir); err != nil {
		t.Fatalf("clearing the archive directory %s: %v", installArchiveDir, err)
	}
	if err := os.MkdirAll(installArchiveDir, 0o755); err != nil {
		t.Fatalf("creating the archive directory %s: %v", installArchiveDir, err)
	}

	staged := syscontainer.ArtifactPath(stagedArchiveRel(t))
	files := []struct {
		source string
		name   string
		mode   os.FileMode
	}{
		{installSystemPrefix, "agent-peer", 0o755},
		{filepath.Join(staged, "install.sh"), "install.sh", 0o755},
		{filepath.Join(staged, "LICENSE"), "LICENSE", 0o644},
		{filepath.Join(staged, "README.md"), "README.md", 0o644},
	}
	for _, file := range files {
		if err := copyInstallFile(file.source, filepath.Join(installArchiveDir, file.name), file.mode); err != nil {
			t.Fatalf("building the release archive: %v", err)
		}
	}
	return installArchiveDir
}

// copyInstallFile copies src to dst with mode.
func copyInstallFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}
	return nil
}

// installFakeClaude writes the fake claude into the image's PATH and clears its
// state, so the scenario starts with no registration.
func installFakeClaude(t *testing.T) {
	t.Helper()

	if err := os.RemoveAll(installStateDir); err != nil {
		t.Fatalf("clearing the fake claude's state directory %s: %v", installStateDir, err)
	}
	if err := os.MkdirAll(installStateDir, 0o777); err != nil {
		t.Fatalf("creating the fake claude's state directory %s: %v", installStateDir, err)
	}
	if err := os.Chmod(installStateDir, 0o777); err != nil {
		t.Fatalf("chmod %s: %v", installStateDir, err)
	}
	if err := os.WriteFile(installClaudePath, []byte(installFakeClaudeScript), 0o755); err != nil {
		t.Fatalf("writing the fake claude %s: %v", installClaudePath, err)
	}
	if err := os.Chmod(installClaudePath, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", installClaudePath, err)
	}
}

// removePreinstalledSystemBinary removes the release binary the image already
// carries at the system-level default path, so the owner and mode checked after
// the install are the install's own.
func removePreinstalledSystemBinary(t *testing.T) {
	t.Helper()

	if err := os.Remove(installSystemPrefix); err != nil {
		t.Fatalf("removing the image's built-in %s: %v", installSystemPrefix, err)
	}
	if _, err := os.Stat(installSystemPrefix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists after its removal: %v", installSystemPrefix, err)
	}
}

// configureInstallSudo grants the ordinary account passwordless sudo, which the
// system-level scenario needs to run the script the way an administrator does.
func configureInstallSudo(t *testing.T) {
	t.Helper()

	entry := installAccount + " ALL=(ALL) NOPASSWD: ALL\n"
	if err := os.WriteFile(installSudoersPath, []byte(entry), 0o440); err != nil {
		t.Fatalf("writing %s: %v", installSudoersPath, err)
	}
	if err := os.Chmod(installSudoersPath, 0o440); err != nil {
		t.Fatalf("chmod %s: %v", installSudoersPath, err)
	}
	res := runWithEnv(t, "root", nil, "visudo", "-cf", installSudoersPath)
	if res.ExitCode != 0 {
		t.Fatalf("visudo -cf %s: exit %d\nstdout: %s\nstderr: %s",
			installSudoersPath, res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
}

// startInstallUserManager starts the ordinary account's user manager the way a
// login would and waits for its runtime directory and bus.
func startInstallUserManager(t *testing.T) {
	t.Helper()

	uid := strconv.FormatUint(uint64(installUID), 10)
	mustRun(t, "root", nil, "loginctl", "enable-linger", installAccount)
	mustRun(t, "root", nil, "systemctl", "start", "user@"+uid+".service")
	waitUserManager(t, installUID)
}

// assertInstallScriptRefusesRoot runs the script through sudo without --system
// and checks that it refuses the user-level install as root and leaves the host
// unchanged: no binary, no unit, no MCP registration, no configuration.
func assertInstallScriptRefusesRoot(t *testing.T, archive string) {
	t.Helper()

	res := runInstallScriptAs(t, installAccount, installUserEnv(), archive, "sudo", "-n", "sh", "./install.sh")
	if res.ExitCode == 0 {
		t.Fatalf("sudo sh ./install.sh as %s exited 0, want a refusal\nstdout: %s\nstderr: %s",
			installAccount, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
	combined := res.Stdout + res.Stderr
	for _, want := range []string{"must not run as root", "sudo sh ./install.sh --system"} {
		if !strings.Contains(combined, want) {
			t.Errorf("output = %q, want it to contain %q", combined, want)
		}
	}

	for _, path := range []string{
		installUserPrefix,
		installSystemPrefix,
		installUserUnitPath,
		installSystemUnitPath,
		installUserConfigDir,
		installSystemConfigDir,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a refused install left %s behind: %v", path, err)
		}
	}

	invocations := filepath.Join(installStateDir, "invocations")
	data, err := os.ReadFile(invocations)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "\tmcp add") {
				t.Errorf("a refused install registered the MCP server: %q", line)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading the fake claude's invocation log %s: %v", invocations, err)
	}
}

// runSystemInstallScript runs the script with --system through sudo as the
// ordinary account, so the script sees root and SUDO_USER naming that account.
func runSystemInstallScript(t *testing.T, archive string) {
	t.Helper()

	res := runInstallScriptAs(t, installAccount, installUserEnv(), archive, "sudo", "-n", "sh", "./install.sh", "--system")
	if res.ExitCode != 0 {
		t.Fatalf("sudo sh ./install.sh --system as %s: exit %d\nstdout: %s\nstderr: %s",
			installAccount, res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
}

// runInstallScriptAs runs name in dir as account with env and returns its
// output and exit code. A process that cannot be started fails the test.
func runInstallScriptAs(t *testing.T, account string, env []string, dir, name string, args ...string) syscontainer.Result {
	t.Helper()

	cmd := syscontainer.Command(account, env, name, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := syscontainer.Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return res
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running %s %s as %s: %v", name, strings.Join(args, " "), account, err)
	}
	res.ExitCode = exitErr.ExitCode()
	return res
}

// assertUserInstall checks the binary, the version output and the running user
// service after a user-level install.
func assertUserInstall(t *testing.T) {
	t.Helper()

	assertInstallBinary(t, installUserPrefix, installUID)
	assertInstallVersion(t, installAccount, installUserEnv(), installUserPrefix)
	waitInstallSocket(t, "/tmp/agent-peer-"+strconv.FormatUint(uint64(installUID), 10)+"/broker.sock")
	waitInstallUnitState(t, installAccount, installUserEnv(), "active", "--user")

	found := false
	for _, proc := range syscontainer.Pids("agent-peer", "broker") {
		if proc.UID == installUID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Broker process runs as uid %d: %v", installUID, syscontainer.Pids("agent-peer", "broker"))
	}
}

// assertSystemInstall checks the binary, the version output and the running
// system service after a system-level install.
func assertSystemInstall(t *testing.T) {
	t.Helper()

	uid, gid, mode := fileOwnerAndMode(t, installSystemPrefix)
	if uid != 0 || gid != 0 {
		t.Errorf("%s owner = %d:%d, want 0:0", installSystemPrefix, uid, gid)
	}
	if mode != 0o755 {
		t.Errorf("%s mode = %#o, want 0755", installSystemPrefix, mode)
	}
	if mode&0o022 != 0 {
		t.Errorf("%s mode = %#o, want no group or other write permission", installSystemPrefix, mode)
	}
	assertInstallVersion(t, "root", nil, installSystemPrefix)
	waitInstallSocket(t, "/run/agent-peer/broker.sock")
	waitInstallUnitState(t, "root", nil, "active")

	if procs := syscontainer.Pids("agent-peer", "broker", "--system"); len(procs) == 0 {
		t.Fatalf("no system Broker process is running")
	}
}

// assertInstallBinary checks that path is a regular 0755 file owned by wantUID.
func assertInstallBinary(t *testing.T, path string, wantUID uint32) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", path, info.Mode())
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("%s mode = %#o, want 0755", path, got)
	}
	uid, _, _ := fileOwnerAndMode(t, path)
	if uid != wantUID {
		t.Errorf("%s owner = %d, want %d", path, uid, wantUID)
	}
}

// assertInstallVersion runs the installed binary with --version and checks that
// it reports the version the test build was stamped with.
func assertInstallVersion(t *testing.T, account string, env []string, binary string) {
	t.Helper()

	res := runWithEnv(t, account, env, binary, "--version")
	if res.ExitCode != 0 {
		t.Fatalf("%s --version as %s: exit %d\nstdout: %s\nstderr: %s",
			binary, account, res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
	if out := strings.TrimSpace(res.Stdout); out != installExpectedVersion {
		t.Errorf("%s --version as %s = %q, want %q", binary, account, out, installExpectedVersion)
	}
}

// waitInstallSocket waits for a Broker socket to appear.
func waitInstallSocket(t *testing.T, socket string) {
	t.Helper()

	harness.Eventually(t, installServiceStartBound, "the Broker socket "+socket, func() (bool, string) {
		if _, err := os.Stat(socket); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

// waitInstallUnitState waits until systemctl reports want for the agent-peer
// unit. scopeArgs selects the user manager ("--user") or the system manager.
func waitInstallUnitState(t *testing.T, account string, env []string, want string, scopeArgs ...string) {
	t.Helper()

	args := append(append([]string{}, scopeArgs...), "is-active", "agent-peer.service")
	harness.Eventually(t, installServiceStartBound, "agent-peer.service to be "+want, func() (bool, string) {
		res := runWithEnv(t, account, env, "systemctl", args...)
		state := strings.TrimSpace(res.Stdout)
		return state == want, fmt.Sprintf("is-active = %q (exit %d, stderr %s)",
			state, res.ExitCode, strings.TrimSpace(res.Stderr))
	})
}

// assertInstallMCPAdd checks that the fake claude received exactly want add
// calls, all as wantUID with wantHome, and that each named the binary. Every
// invocation whose arguments start with "mcp add" counts, so an install that
// registered twice under different arguments cannot hide behind the alias.
func assertInstallMCPAdd(t *testing.T, want int, binary string, wantUID uint32, wantHome string) {
	t.Helper()

	wantArgs := " -- " + binary + " mcp"
	calls := installMCPAddCalls(t)
	if len(calls) != want {
		t.Fatalf("fake claude saw %d mcp add calls, want %d; invocations:\n%s",
			len(calls), want, installClaudeLog(t))
	}
	for _, call := range calls {
		if !strings.HasSuffix(call.Args, wantArgs) {
			t.Errorf("mcp add call %q does not end with %q", call.Args, wantArgs)
		}
		if call.UID != wantUID {
			t.Errorf("%q ran as uid %d, want %d", call.Args, call.UID, wantUID)
		}
		if call.Home != wantHome {
			t.Errorf("%q ran with HOME %q, want %q", call.Args, call.Home, wantHome)
		}
	}
}

// installMCPAddCalls returns every fake claude invocation that starts with
// "mcp add", whatever arguments follow.
func installMCPAddCalls(t *testing.T) []installClaudeCall {
	t.Helper()

	var matching []installClaudeCall
	for _, call := range readInstallClaudeCalls(t) {
		if strings.HasPrefix(call.Args, "mcp add") {
			matching = append(matching, call)
		}
	}
	return matching
}

// assertInstallMCPGet checks that the fake claude received exactly want get
// calls, all as wantUID with wantHome. The second install relies on the answer
// the first registration left behind.
func assertInstallMCPGet(t *testing.T, want int, wantUID uint32, wantHome string) {
	t.Helper()

	const wantArgs = "mcp get peer"
	calls := installMCPCalls(t, wantArgs)
	if len(calls) != want {
		t.Fatalf("fake claude saw %q %d times, want %d; invocations:\n%s",
			wantArgs, len(calls), want, installClaudeLog(t))
	}
	for _, call := range calls {
		if call.UID != wantUID {
			t.Errorf("%q ran as uid %d, want %d", wantArgs, call.UID, wantUID)
		}
		if call.Home != wantHome {
			t.Errorf("%q ran with HOME %q, want %q", wantArgs, call.Home, wantHome)
		}
	}
}

// installMCPCalls returns the fake claude's invocations whose arguments equal
// args.
func installMCPCalls(t *testing.T, args string) []installClaudeCall {
	t.Helper()

	var matching []installClaudeCall
	for _, call := range readInstallClaudeCalls(t) {
		if call.Args == args {
			matching = append(matching, call)
		}
	}
	return matching
}

// readInstallClaudeCalls reads and parses the fake claude's invocation log.
func readInstallClaudeCalls(t *testing.T) []installClaudeCall {
	t.Helper()

	path := filepath.Join(installStateDir, "invocations")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fake claude's invocation log %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	calls := make([]installClaudeCall, 0, len(lines))
	for i, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("invocation %d has %d fields, want 5: %q", i+1, len(fields), line)
		}
		uid, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			t.Fatalf("invocation %d has a non-numeric uid %q: %v", i+1, fields[0], err)
		}
		calls = append(calls, installClaudeCall{
			UID:      uint32(uid),
			User:     fields[1],
			Home:     fields[2],
			SudoUser: fields[3],
			Args:     fields[4],
		})
	}
	return calls
}

// installClaudeLog renders the fake claude's invocations for a failure message.
func installClaudeLog(t *testing.T) string {
	t.Helper()

	calls := readInstallClaudeCalls(t)
	var b strings.Builder
	for _, call := range calls {
		fmt.Fprintf(&b, "uid=%d user=%q home=%q sudo_user=%q args=%q\n",
			call.UID, call.User, call.Home, call.SudoUser, call.Args)
	}
	return b.String()
}
