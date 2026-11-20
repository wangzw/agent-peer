//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// TestRegistrationListAgentsAbandonsAStalledCall pins the timing condition of
// the unusable-name list timeout without a busy machine: the Broker accepts the
// session's registration and then never answers ListAgents. A list_agents call
// made from a registration wait must give up at the wait's call bound and
// report a retryable state. When the call used the harness's sixty-second call
// bound instead, one stalled call outlived the wait and failed the test from
// inside it with a transport error.
func TestRegistrationListAgentsAbandonsAStalledCall(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	socket := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, socket, fakelocal.Handlers{
		Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
			return &localpb.HelloResponse{ProtocolVersion: localpb.ProtocolVersion}, nil
		},
		// The Broker accepts the request and never answers it until the call is
		// cancelled or the test releases it.
		ListAgents: func(ctx context.Context, _ *localpb.ListAgentsRequest) (*localpb.ListAgentsResponse, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return &localpb.ListAgentsResponse{}, nil
		},
		Send: func(context.Context, *localpb.SendRequest) (*localpb.SendResponse, error) {
			return &localpb.SendResponse{}, nil
		},
	})

	host := harness.NewLocalHost(t, harness.LocalOptions{Name: "stalled-listing"})
	s := host.StartSession(t, harness.SessionSpec{
		Name:     "stalled",
		StartMCP: true,
		Env:      map[string]string{"AGENT_PEER_TEST_SOCKET": socket},
	})
	defer s.End(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := srv.NextRegister(ctx)
	if err != nil {
		t.Fatalf("waiting for the session's Register stream: %v", err)
	}
	if err := conn.Complete("agent-under-test", "stalled@test", false); err != nil {
		t.Fatalf("completing registration: %v", err)
	}

	// send_message needs the agent identity, so its success proves the MCP
	// server applied registration_complete. list_agents would prove it too, but
	// it is the call under test.
	harness.Eventually(t, 3*time.Second, "the session is registered", func() (bool, string) {
		r := s.Call(t, "send_message", map[string]any{"to": "someone@test", "message": "hi"})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})

	start := time.Now()
	_, state := registrationListAgentsInWait(t, s, start.Add(harness.ToolCallPollBound), nil)
	elapsed := time.Since(start)

	if state == "" {
		t.Fatal("list_agents succeeded although the Broker never answered")
	}
	if !strings.Contains(state, "deadline exceeded") {
		t.Fatalf("the stalled call did not report its bound: %s", state)
	}
	if limit := 2 * harness.ToolCallPollBound; elapsed > limit {
		t.Fatalf("the stalled call returned after %s, want at most %s: %s", elapsed, limit, state)
	}
}

// TestWaitRegisteredToleratesASlowUpdateCheck checks the poll bound against the
// update hint. The hint runs inside the same tools/call after the handler and
// may spend almost updateStatusTimeout, three seconds, querying the Broker;
// with a poll bound equal to that timeout every call would be cut off while the
// hint was answering. The fake Broker here spends 2.9 seconds in UpdateStatus
// and another second in ListAgents, so the call lasts more than three seconds
// but stays well inside ToolCallPollBound, and WaitRegistered must still pass.
func TestWaitRegisteredToleratesASlowUpdateCheck(t *testing.T) {
	const (
		updateCheckDelay = 2900 * time.Millisecond
		handlerDelay     = time.Second
	)
	srv := fakelocal.Start(t, fakelocal.SocketPath(t), fakelocal.Handlers{
		ListAgents: func(context.Context, *localpb.ListAgentsRequest) (*localpb.ListAgentsResponse, error) {
			time.Sleep(handlerDelay)
			return &localpb.ListAgentsResponse{}, nil
		},
		UpdateStatus: func(context.Context, *localpb.UpdateStatusRequest) (*localpb.UpdateStatusResponse, error) {
			time.Sleep(updateCheckDelay)
			return &localpb.UpdateStatusResponse{}, nil
		},
	})
	s, _ := registrationStalledSession(t, srv, "slow-update", "slow-agent", "slow@test")

	start := time.Now()
	harness.WaitRegistered(t, s, harness.ToolCallWaitBound)
	elapsed := time.Since(start)
	if elapsed < handlerDelay+updateCheckDelay {
		t.Fatalf("WaitRegistered returned after %s, want at least the %s update check and %s handler delays; the call did not exercise the hint", elapsed, updateCheckDelay, handlerDelay)
	}
	t.Logf("WaitRegistered passed after %s with a %s update check", elapsed, updateCheckDelay)
}

// registrationStalledSession starts a session whose MCP server talks to srv,
// waits for its Register stream and completes the registration as agentID at
// address. The session ends with the test.
func registrationStalledSession(t *testing.T, srv *fakelocal.Server, name, agentID, address string) (*harness.Session, *fakelocal.RegisterConn) {
	t.Helper()
	host := harness.NewLocalHost(t, harness.LocalOptions{Name: "stalled-" + name})
	s := host.StartSession(t, harness.SessionSpec{
		Name:     name,
		StartMCP: true,
		Env:      map[string]string{"AGENT_PEER_TEST_SOCKET": srv.Path()},
	})
	t.Cleanup(func() { s.End(t) })

	ctx, cancel := context.WithTimeout(context.Background(), harness.ToolCallPollBound)
	defer cancel()
	conn, err := srv.NextRegister(ctx)
	if err != nil {
		t.Fatalf("waiting for the session's Register stream: %v", err)
	}
	if err := conn.Complete(agentID, address, false); err != nil {
		t.Fatalf("completing registration: %v", err)
	}
	return s, conn
}
