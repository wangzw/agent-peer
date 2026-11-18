//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

// TestHarnessAccountsAndBinary checks that the container provides the release
// binary and the two accounts with the supplementary groups the permission
// tests rely on.
func TestHarnessAccountsAndBinary(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	version := syscontainer.RunAs(t, "peer", nil, "--version")
	if version.ExitCode != 0 {
		t.Fatalf("agent-peer --version as peer: exit %d\n%s", version.ExitCode, version.Stderr)
	}
	if !strings.Contains(version.Stdout, "agent-peer e2e") {
		t.Errorf("agent-peer --version output = %q, want it to contain %q", version.Stdout, "agent-peer e2e")
	}

	peerUID := syscontainer.AccountUID(t, "peer")
	if got := runID(t, "peer", "-u"); got != strconv.FormatUint(uint64(peerUID), 10) {
		t.Errorf("id -u as peer = %q, want %d", got, peerUID)
	}
	if groups := runID(t, "peer", "-G"); !hasGroup(groups, "2001") {
		t.Errorf("id -G as peer = %q, want it to contain 2001", groups)
	}

	if got := syscontainer.AccountUID(t, "other"); got != 2000 {
		t.Errorf("AccountUID(other) = %d, want 2000", got)
	}
	if got := runID(t, "other", "-u"); got != "2000" {
		t.Errorf("id -u as other = %q, want 2000", got)
	}
	if groups := runID(t, "other", "-G"); hasGroup(groups, "2001") {
		t.Errorf("id -G as other = %q, want it not to contain 2001", groups)
	}
}

// TestHarnessSystemBrokerAndSessions starts the system Broker and reaches it
// from two accounts' sessions. It checks the process and socket the design
// places under /run/agent-peer.
func TestHarnessSystemBrokerAndSessions(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	broker := syscontainer.StartSystemBroker(t, "")
	if _, err := os.Stat("/run/agent-peer/broker.sock"); err != nil {
		t.Fatalf("the system Broker socket is not at /run/agent-peer/broker.sock: %v", err)
	}
	found := false
	for _, proc := range syscontainer.Pids("agent-peer", "broker", "--system") {
		if proc.PID == broker.PID {
			found = true
		}
	}
	if !found {
		t.Errorf("process %d does not appear in Pids(agent-peer, broker, --system)", broker.PID)
	}

	a := syscontainer.NewSession(t, "peer", "a1")
	b := syscontainer.NewSession(t, "other", "b1")
	for _, session := range []*syscontainer.Session{a, b} {
		waitRegistered(t, session)
		if text, isError := session.Call(t, "join_topic", map[string]any{"name": "e2e/smoke"}); isError {
			t.Fatalf("join_topic as %s reported an error: %s", session.Account, text)
		}
	}
	if text, isError := a.Call(t, "list_agents", map[string]any{"topic": "e2e/smoke"}); isError {
		t.Fatalf("list_agents as peer reported an error: %s", text)
	}
}

// TestHarnessSystemService installs the system service through systemd and
// starts it, so the tests downstream that exercise a managed system Broker can
// rely on StartSystemService. It checks the unit is active and that the Broker
// created its socket under /run/agent-peer.
func TestHarnessSystemService(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	syscontainer.StartSystemService(t, "")

	if _, err := os.Stat("/run/agent-peer/broker.sock"); err != nil {
		t.Fatalf("the system service socket is not at /run/agent-peer/broker.sock: %v", err)
	}
	active := runCommandAs(t, "root", "systemctl", "is-active", "agent-peer.service")
	if state := strings.TrimSpace(active.Stdout); state != "active" {
		t.Errorf("systemctl is-active agent-peer.service = %q (exit %d), want %q\nstderr: %s",
			state, active.ExitCode, "active", strings.TrimSpace(active.Stderr))
	}
}

// waitRegistered waits until the MCP server has registered the session with the
// Broker. Registration happens in the background after the MCP handshake.
func waitRegistered(t *testing.T, session *syscontainer.Session) {
	t.Helper()
	syscontainer.WaitFor(t, "session "+session.Account+" to register", func() bool {
		_, isError := session.Call(t, "list_agents", map[string]any{})
		return !isError
	})
}

// runID runs id as account and returns its trimmed standard output.
func runID(t *testing.T, account string, args ...string) string {
	t.Helper()
	out, err := syscontainer.Command(account, nil, "id", args...).Output()
	if err != nil {
		t.Fatalf("id %s as %s: %v", strings.Join(args, " "), account, err)
	}
	return strings.TrimSpace(string(out))
}

// hasGroup reports whether the space-separated group list contains gid.
func hasGroup(groups, gid string) bool {
	for _, field := range strings.Fields(groups) {
		if field == gid {
			return true
		}
	}
	return false
}

// TestHarnessInnerFailurePropagates checks that a failure inside the container
// makes the host test fail rather than pass silently.
func TestHarnessInnerFailurePropagates(t *testing.T) {
	if syscontainer.InContainer() {
		t.Skip("the propagation check runs on the host")
	}

	cmd := exec.Command("go", "test", "-tags", "e2e", "-count=1", "-run", "^TestHarnessAlwaysFails$", ".")
	cmd.Env = append(os.Environ(), "AGENT_PEER_E2E_EXPECT_FAIL=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the inner container failure was not propagated; output:\n%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running the inner test: %v", err)
	}
}

// TestHarnessAlwaysFails fails inside the container when the propagation smoke
// test asks for it and skips otherwise.
func TestHarnessAlwaysFails(t *testing.T) {
	if os.Getenv("AGENT_PEER_E2E_EXPECT_FAIL") != "1" {
		t.Skip("only run by TestHarnessInnerFailurePropagates")
	}
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}
	t.Fatal("intentional failure inside the plain container")
}
