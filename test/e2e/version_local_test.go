//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/node"
)

// versionLocalLaunchBudget is the ten-second readiness allowance of a launched
// Broker plus three seconds.
const versionLocalLaunchBudget = 13 * time.Second

// versionLocalBrokerProcs returns the node's agent-peer broker processes. The
// environment is the filter because a Broker launched by an MCP server process
// inherits it too.
func versionLocalBrokerProcs(n *harness.Node) ([]node.Proc, error) {
	home := n.AccountHome()
	return node.ListProcs(func(p node.Proc) bool {
		if p.Env["AGENT_PEER_TEST_HOME"] != home {
			return false
		}
		return len(p.Args) >= 2 && p.Args[1] == "broker"
	})
}

// versionLocalMustBrokerProcs is versionLocalBrokerProcs for a call outside an
// Eventually condition.
func versionLocalMustBrokerProcs(t *testing.T, n *harness.Node) []node.Proc {
	t.Helper()
	procs, err := versionLocalBrokerProcs(n)
	if err != nil {
		t.Fatalf("listing the agent-peer broker processes: %v", err)
	}
	return procs
}

// versionLocalLogRecord returns the last JSON log record whose event field is
// event, or nil when the log has none.
func versionLocalLogRecord(log []byte, event string) map[string]any {
	var found map[string]any
	for _, line := range strings.Split(string(log), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record["event"] == event {
			found = record
		}
	}
	return found
}

// versionLocalAddressName returns the name part of an agent address.
func versionLocalAddressName(addr string) string {
	if at := strings.LastIndexByte(addr, '@'); at >= 0 {
		return addr[:at]
	}
	return addr
}

// versionLocalSetup starts a node whose Broker speaks the default local
// protocol version, starts session a at that version, and starts session b at
// version 2. It waits until b reports the mismatch so the caller does not race
// the background Broker selection.
func versionLocalSetup(t *testing.T) (*harness.Node, harness.BrokerStart, *harness.Session, *harness.Session) {
	t.Helper()

	n := harness.NewLocalHost(t, harness.LocalOptions{Name: "v"})
	n.WriteConfig(t, "broker:\n  listen: "+n.FreeAddr(t)+"\n")
	st := n.StartBroker(t, harness.BrokerSpec{})
	if !st.Running {
		t.Fatalf("the Broker did not start: exit code %d: %s", st.ExitCode, strings.TrimSpace(st.StderrLast))
	}
	if _, detail := n.Oracle().Status(t); detail != nil {
		t.Fatalf("Status returned %s: %s", detail.GetCode(), detail.GetMessage())
	}

	a := n.StartSession(t, harness.SessionSpec{Name: "a", StartMCP: true})
	harness.Eventually(t, 3*time.Second, "a is registered", func() (bool, string) {
		r := a.Call(t, "list_agents", nil)
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})

	b := n.StartSession(t, harness.SessionSpec{
		Name:     "b",
		StartMCP: true,
		Env:      map[string]string{"AGENT_PEER_TEST_LOCAL_PROTOCOL_VERSION": "2"},
	})

	harness.Eventually(t, 3*time.Second, "b reports the local protocol version mismatch", func() (bool, string) {
		r := b.Call(t, "list_agents", nil)
		if !r.IsError {
			return false, "list_agents succeeded"
		}
		if r.Code() != "version_mismatch" {
			return false, r.Text
		}
		return true, ""
	})
	return n, st, a, b
}

// TestLocalVersionMismatchReq016 checks the REQ-broker-config-016 acceptance:
// a session whose MCP server process speaks another local protocol version
// fails every tool call with version_mismatch, the Broker keeps running and
// keeps its registered agents, and no second Broker appears.
func TestLocalVersionMismatchReq016(t *testing.T) {
	n, st, a, b := versionLocalSetup(t)

	for i := 0; i < 3; i++ {
		r := b.Call(t, "list_agents", nil)
		if !r.IsError {
			t.Fatalf("list_agents %d succeeded against a Broker at another version: %s", i+1, r.Text)
		}
		if got := r.Code(); got != "version_mismatch" {
			t.Fatalf("list_agents %d error code = %q, want version_mismatch; result: %s", i+1, got, r.Text)
		}
		if !strings.Contains(r.Text, "restart the Broker") {
			t.Fatalf("list_agents %d error = %q, want a \"restart the Broker\" hint", i+1, r.Text)
		}
	}

	procs := versionLocalMustBrokerProcs(t, n)
	if len(procs) != 1 {
		t.Fatalf("the node runs %d agent-peer broker processes, want 1: %v", len(procs), procs)
	}
	if procs[0].PID != st.PID {
		t.Fatalf("the Broker process is %d, want the one that was started: %d", procs[0].PID, st.PID)
	}

	resp, detail := n.Oracle().Status(t)
	if detail != nil {
		t.Fatalf("Status returned %s: %s", detail.GetCode(), detail.GetMessage())
	}
	if got := len(resp.GetAgents()); got != 1 {
		t.Fatalf("the Broker reports %d agents, want only a: %v", got, resp.GetAgents())
	}
	if name := versionLocalAddressName(resp.GetAgents()[0].GetAddress()); name != "a" {
		t.Fatalf("the registered agent is %q, want a", name)
	}

	a.Call(t, "list_agents", nil).MustOK(t)

	record := versionLocalLogRecord(n.BrokerLog(t), "hello_version_mismatch")
	if record == nil {
		t.Fatal("the Broker log has no hello_version_mismatch record")
	}
	if got := record["client_version"]; got != float64(2) {
		t.Errorf("client_version = %v, want 2", got)
	}
	if got := record["broker_version"]; got != float64(1) {
		t.Errorf("broker_version = %v, want 1", got)
	}
}

// TestLocalVersionMismatchRecoveryReq016 checks the other half of
// REQ-broker-config-016: after the incompatible Broker stops, b's MCP server
// process starts a Broker at its own version and the session recovers by
// itself.
func TestLocalVersionMismatchRecoveryReq016(t *testing.T) {
	n, st, a, b := versionLocalSetup(t)

	a.End(t)
	if code := n.StopBroker(t, syscall.SIGTERM); code != 0 {
		t.Fatalf("the Broker exited %d on SIGTERM, want 0", code)
	}

	var freshPID int
	harness.Eventually(t, versionLocalLaunchBudget, "b's MCP server process starts its own Broker and recovers", func() (bool, string) {
		procs, err := versionLocalBrokerProcs(n)
		if err != nil {
			return false, err.Error()
		}
		procs = versionLocalWithout(procs, st.PID)
		if len(procs) == 0 {
			return false, "no Broker process newer than the stopped one"
		}
		if len(procs) > 1 {
			return false, fmt.Sprintf("%d Broker processes are running", len(procs))
		}
		if got := procs[0].Env["AGENT_PEER_TEST_LOCAL_PROTOCOL_VERSION"]; got != "2" {
			return false, fmt.Sprintf("the new Broker speaks local protocol version %q, want 2", got)
		}
		r := b.Call(t, "list_agents", nil)
		if r.IsError {
			return false, r.Text
		}
		freshPID = procs[0].PID
		return true, ""
	})
	if freshPID == 0 {
		t.Fatal("the recovery Broker has no PID")
	}

	b.Call(t, "list_agents", nil).MustOK(t)
	if procs := versionLocalMustBrokerProcs(t, n); len(procs) != 1 {
		t.Fatalf("the node runs %d agent-peer broker processes after recovery, want 1: %v", len(procs), procs)
	}
}

// versionLocalWithout returns procs without the process whose PID is pid.
func versionLocalWithout(procs []node.Proc, pid int) []node.Proc {
	out := procs[:0]
	for _, p := range procs {
		if p.PID != pid {
			out = append(out, p)
		}
	}
	return out
}
