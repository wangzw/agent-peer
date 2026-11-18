//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// srSystemSocket is the system Broker's local socket.
	srSystemSocket = "/run/agent-peer/broker.sock"
	// srSystemService is the systemd unit of the system-level Broker.
	srSystemService = "agent-peer.service"
)

// srInit starts the systemd container, unless this is the inner run, and
// reports whether the test should continue inside the container.
func srInit(t *testing.T) bool {
	t.Helper()
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return false
	}
	return true
}

// srSetup starts the system service and peer's user-level Broker at :17444,
// with peer's configuration naming the same address.
func srSetup(t *testing.T) *syscontainer.Proc {
	t.Helper()
	syscontainer.StartSystemService(t, "")
	srWriteAccountConfig(t, "peer", "broker:\n  listen: \":17444\"\n")
	return syscontainer.StartUserBroker(t, "peer", "--listen", ":17444")
}

// srWriteAccountConfig writes config.yaml for account, owned by that account
// with the modes the design requires.
func srWriteAccountConfig(t *testing.T, account, content string) {
	t.Helper()
	u, err := user.Lookup(account)
	if err != nil {
		t.Fatalf("looking up account %q: %v", account, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		t.Fatalf("account %q has a non-numeric uid %q", account, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Fatalf("account %q has a non-numeric gid %q", account, u.Gid)
	}
	dir := filepath.Join(u.HomeDir, ".agent-peer")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		t.Fatalf("chown %s: %v", dir, err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		t.Fatalf("chown %s: %v", path, err)
	}
}

// srWaitRegistered waits until the session has registered with its Broker.
func srWaitRegistered(t *testing.T, s *syscontainer.Session, what string) {
	t.Helper()
	syscontainer.WaitFor(t, "session "+what+" to register", func() bool {
		_, isError := s.Call(t, "list_agents", map[string]any{})
		return !isError
	})
}

// srMustStatus returns the output of agent-peer status as account and fails
// when the command does not succeed.
func srMustStatus(t *testing.T, account string) string {
	t.Helper()
	res := syscontainer.RunAs(t, account, nil, "status")
	if res.ExitCode != 0 {
		t.Fatalf("status as %s exited %d: %s", account, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout
}

// srAgentAddress returns the address column of the status row whose address
// contains name, or "" when there is no such row.
func srAgentAddress(status, name string) string {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.Contains(fields[0], name) {
			return fields[0]
		}
	}
	return ""
}

// srIdentityHash returns the lowercase hex SHA-256 of account's identity key.
func srIdentityHash(t *testing.T, account string) string {
	t.Helper()
	u, err := user.Lookup(account)
	if err != nil {
		t.Fatalf("looking up account %q: %v", account, err)
	}
	path := filepath.Join(u.HomeDir, ".agent-peer", "identity.key")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// srBrokerProcs returns the Broker processes of account.
func srBrokerProcs(t *testing.T, account string) []syscontainer.ProcInfo {
	t.Helper()
	uid := syscontainer.AccountUID(t, account)
	var out []syscontainer.ProcInfo
	for _, proc := range syscontainer.Pids("agent-peer", "broker") {
		if proc.UID == uid {
			out = append(out, proc)
		}
	}
	return out
}

// srStopSystemService stops the system Broker through systemd.
func srStopSystemService(t *testing.T) {
	t.Helper()
	cmd := syscontainer.Command("root", nil, "systemctl", "stop", srSystemService)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("systemctl stop %s: %v\n%s", srSystemService, err, strings.TrimSpace(string(out)))
	}
}

// srLeaveStaleSocket leaves an unlistened socket file at the system path, the
// state a stopped Broker can leave behind.
func srLeaveStaleSocket(t *testing.T) {
	t.Helper()
	if err := os.Remove(srSystemSocket); err != nil && !os.IsNotExist(err) {
		t.Fatalf("removing %s: %v", srSystemSocket, err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: srSystemSocket, Net: "unix"})
	if err != nil {
		t.Fatalf("binding %s: %v", srSystemSocket, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("closing %s: %v", srSystemSocket, err)
	}
	if err := os.Chmod(srSystemSocket, 0o666); err != nil {
		t.Fatalf("chmod %s: %v", srSystemSocket, err)
	}
}

// srMainPID returns the MainPID systemd reports for the system service.
func srMainPID() (int, error) {
	out, err := syscontainer.Command("root", nil, "systemctl", "show", "-p", "MainPID", srSystemService).Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "MainPID=")
		if !ok {
			continue
		}
		return strconv.Atoi(value)
	}
	return 0, fmt.Errorf("systemctl show printed no MainPID")
}

// TestSelectOrderReq026 checks REQ-broker-config-026: without an explicit
// socket a session uses its account's running user-level Broker, an explicit
// AGENT_PEER_SOCKET wins, and an account without a user-level Broker uses the
// system-level one. It also checks that a stale system socket with no process
// behind it makes the session start its own user-level Broker.
func TestSelectOrderReq026(t *testing.T) {
	if !srInit(t) {
		return
	}
	srSetup(t)

	// 1. peer's session, no socket named: peer's user-level Broker.
	s1 := syscontainer.NewSession(t, "peer", "s1")
	srWaitRegistered(t, s1, "s1")
	if strings.Contains(srMustStatus(t, "root"), "s1") {
		t.Fatal("s1 registered on the system Broker although peer's user-level Broker was running")
	}
	if !strings.Contains(srMustStatus(t, "peer"), "s1") {
		t.Fatal("s1 is not on peer's user-level Broker")
	}

	// 2. peer's session with AGENT_PEER_SOCKET: the system Broker.
	s2 := syscontainer.NewSession(t, "peer", "s2", syscontainer.WithEnv("AGENT_PEER_SOCKET="+srSystemSocket))
	srWaitRegistered(t, s2, "s2")
	if !strings.Contains(srMustStatus(t, "root"), "s2") {
		t.Fatal("s2 is not on the system Broker although AGENT_PEER_SOCKET named it")
	}
	if strings.Contains(srMustStatus(t, "peer"), "s2") {
		t.Fatal("s2 fell back to peer's user-level Broker instead of the named socket")
	}

	// 3. an account without a user-level Broker: the system Broker.
	s3 := syscontainer.NewSession(t, "other", "s3")
	srWaitRegistered(t, s3, "s3")
	if !strings.Contains(srMustStatus(t, "root"), "s3") {
		t.Fatal("s3 is not on the system Broker")
	}
	if procs := srBrokerProcs(t, "other"); len(procs) != 0 {
		t.Fatalf("other runs a user-level Broker although the system Broker was available: %v", procs)
	}

	// 4. the system Broker stops and leaves its socket file behind; the stale
	// socket must not be treated as a usable Broker.
	srStopSystemService(t)
	srLeaveStaleSocket(t)
	s4 := syscontainer.NewSession(t, "other", "s4")
	harness.Eventually(t, 13*time.Second, "other's session starts a user-level Broker and registers", func() (bool, string) {
		if procs := srBrokerProcs(t, "other"); len(procs) == 0 {
			return false, "other has no user-level Broker process"
		}
		if _, isError := s4.Call(t, "list_agents", map[string]any{}); isError {
			return false, "list_agents still fails"
		}
		return true, ""
	})
}

// TestReregisterUserBrokerReq037 checks the user-level half of
// REQ-broker-config-037: after its Broker is killed, a session registers again
// with a new Broker of the same account, with the same identity and Agent
// address, and does not move to the system Broker.
func TestReregisterUserBrokerReq037(t *testing.T) {
	if !srInit(t) {
		return
	}
	userBroker := srSetup(t)

	s1 := syscontainer.NewSession(t, "peer", "s1")
	srWaitRegistered(t, s1, "s1")
	status := srMustStatus(t, "peer")
	wantAddress := srAgentAddress(status, "s1")
	if wantAddress == "" {
		t.Fatal("peer's status does not list s1")
	}
	wantKey := srIdentityHash(t, "peer")

	if err := syscall.Kill(userBroker.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("killing peer's user-level Broker %d: %v", userBroker.PID, err)
	}

	harness.Eventually(t, 13*time.Second, "peer's session registers again with a new user-level Broker", func() (bool, string) {
		var fresh []syscontainer.ProcInfo
		for _, proc := range srBrokerProcs(t, "peer") {
			if proc.PID != userBroker.PID {
				fresh = append(fresh, proc)
			}
		}
		if len(fresh) != 1 {
			return false, fmt.Sprintf("%d new user-level Broker processes", len(fresh))
		}
		if _, isError := s1.Call(t, "list_agents", map[string]any{}); isError {
			return false, "list_agents still fails"
		}
		return true, ""
	})

	if got := srIdentityHash(t, "peer"); got != wantKey {
		t.Fatalf("identity.key changed across the Broker restart: sha256 %s, want %s", got, wantKey)
	}
	status = srMustStatus(t, "peer")
	if got := srAgentAddress(status, "s1"); got != wantAddress {
		t.Fatalf("s1's Agent address after the restart is %q, want %q", got, wantAddress)
	}
	if strings.Contains(srMustStatus(t, "root"), "s1") {
		t.Fatal("s1 moved to the system Broker after its user-level Broker was killed")
	}
}

// TestReregisterSystemBrokerReq037 checks the system half of
// REQ-broker-config-037: systemd restarts the system Broker, the session
// registers again with it, and the session never starts a user-level Broker
// while the system Broker is down.
func TestReregisterSystemBrokerReq037(t *testing.T) {
	if !srInit(t) {
		return
	}
	syscontainer.StartSystemService(t, "")

	s3 := syscontainer.NewSession(t, "other", "s3")
	srWaitRegistered(t, s3, "s3")
	if !strings.Contains(srMustStatus(t, "root"), "s3") {
		t.Fatal("s3 is not on the system Broker")
	}
	if procs := srBrokerProcs(t, "other"); len(procs) != 0 {
		t.Fatalf("other runs a user-level Broker although the system Broker was available: %v", procs)
	}

	oldPID, err := srMainPID()
	if err != nil {
		t.Fatalf("reading the system Broker's MainPID: %v", err)
	}
	if oldPID == 0 {
		t.Fatal("systemd reports MainPID 0 before the kill")
	}
	kill := syscontainer.Command("root", nil, "systemctl", "kill", "--signal=SIGKILL", srSystemService)
	if out, err := kill.CombinedOutput(); err != nil {
		t.Fatalf("systemctl kill %s: %v\n%s", srSystemService, err, strings.TrimSpace(string(out)))
	}

	var newPID int
	harness.Eventually(t, 8*time.Second, "systemd restarts the system Broker and s3 registers again", func() (bool, string) {
		pid, err := srMainPID()
		if err != nil {
			return false, err.Error()
		}
		if pid == 0 || pid == oldPID {
			return false, fmt.Sprintf("MainPID is %d, want a pid other than %d", pid, oldPID)
		}
		if _, isError := s3.Call(t, "list_agents", map[string]any{}); isError {
			return false, "list_agents still fails"
		}
		newPID = pid
		return true, ""
	})
	if newPID == oldPID {
		t.Fatal("the system Broker was not restarted")
	}
	if !strings.Contains(srMustStatus(t, "root"), "s3") {
		t.Fatal("s3 is not registered on the restarted system Broker")
	}
	if procs := srBrokerProcs(t, "other"); len(procs) != 0 {
		t.Fatalf("other started a user-level Broker while the system Broker was down: %v", procs)
	}
}
