//go:build e2e

package e2e

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// externalProbeIP is in the TEST-NET-2 documentation range. It can never be
	// a real service, so a successful connection would mean the container
	// found a way out.
	externalProbeIP      = "198.51.100.1"
	externalProbeAddress = externalProbeIP + ":443"
	// gatewayProbePorts are ports likely to be open on a host that the
	// container must not reach.
	gatewayProbePort22   = "22"
	gatewayProbePort6443 = "6443"
	// hostGatewayEnv carries the host's default gateway, passed in by
	// syscontainer.Run.
	hostGatewayEnv = "AGENT_PEER_E2E_HOST_GATEWAY"
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

// TestHarnessNoEgress checks that the network declared in
// compose/system/compose.yaml gives the container no way out: there is no
// default route, or an external address has no route, and both an external
// address and the network gateway's usual service ports are unreachable.
func TestHarnessNoEgress(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	routes := runCommandAs(t, "root", "ip", "-4", "route", "show")
	if routes.ExitCode != 0 {
		t.Fatalf("ip -4 route show: exit %d\nstderr: %s", routes.ExitCode, strings.TrimSpace(routes.Stderr))
	}

	hasDefault, _ := parseDefaultRoute(routes.Stdout)
	lookup := runCommandAs(t, "root", "ip", "route", "get", externalProbeIP)
	unreachable := lookup.ExitCode != 0 &&
		strings.Contains(strings.ToLower(lookup.Stdout+lookup.Stderr), "unreachable")
	if hasDefault && !unreachable {
		t.Errorf("the container has a default route and %s is still routable:\nroutes:\n%s\nroute get: exit %d\n%s",
			externalProbeIP, strings.TrimSpace(routes.Stdout), lookup.ExitCode,
			strings.TrimSpace(lookup.Stdout+lookup.Stderr))
	}
	if !hasDefault && !unreachable {
		t.Logf("no default route; route get %s: exit %d, %s", externalProbeIP, lookup.ExitCode,
			strings.TrimSpace(lookup.Stdout+lookup.Stderr))
	}

	assertDialFails(t, externalProbeAddress)

	// The host's own default gateway is the address an egress path would run
	// through first. The host test process passes it in; a host without one,
	// such as a non-Linux host, leaves it empty.
	gateway := os.Getenv(hostGatewayEnv)
	if gateway == "" {
		t.Logf("the host reported no default gateway to probe; routes in the container:\n%s",
			strings.TrimSpace(routes.Stdout))
		return
	}
	assertDialFails(t, net.JoinHostPort(gateway, gatewayProbePort22))
	assertDialFails(t, net.JoinHostPort(gateway, gatewayProbePort6443))
}

// parseDefaultRoute reports whether routes has a default route and returns the
// gateway it names.
func parseDefaultRoute(routes string) (bool, string) {
	for _, line := range strings.Split(routes, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		for i, field := range fields {
			if field == "via" && i+1 < len(fields) {
				return true, fields[i+1]
			}
		}
		return true, ""
	}
	return false, ""
}

// assertDialFails checks that a TCP connection to addr cannot be opened.
func assertDialFails(t *testing.T, addr string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Errorf("dialing %s succeeded, want the container to have no egress", addr)
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
