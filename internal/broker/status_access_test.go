package broker

import (
	"context"
	"testing"

	"github.com/wangzw/peers/internal/broker/localpb"
	"google.golang.org/protobuf/proto"
)

// statusAccessAgents registers two agents, gives them different owners, and
// installs one peer, so a test can see whether Status filters the response.
func statusAccessAgents(t *testing.T, tb *testBroker) (mine, other AgentID) {
	t.Helper()
	mine, _ = statusRegisterAgent(t, tb, "mine")
	other, _ = statusRegisterAgent(t, tb, "other")

	tb.b.mu.Lock()
	tb.b.agents[mine].caller.UID = 1000
	tb.b.agents[other].caller.UID = 1001
	tb.b.mu.Unlock()

	tb.peers.infos = []*localpb.BrokerInfo{{Alias: "hostB", IsPeer: true}}
	return mine, other
}

func TestStatusWithoutReadAccess(t *testing.T) {
	// REQ-broker-config-010
	tb := newTestBroker(t)
	mine, _ := statusAccessAgents(t, tb)

	ctx := withReadGranted(accessCallerContext(1000, "alice"), false)
	resp, err := tb.b.Status(ctx, &localpb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Agents) != 1 || resp.Agents[0].AgentId != string(mine) {
		t.Fatalf("Status agents = %v, want only %s", resp.Agents, mine)
	}
	if len(resp.Peers) == 0 {
		t.Fatalf("Status returned no peers without read access: %v", resp.Peers)
	}

	grantedCtx := withReadGranted(accessCallerContext(1000, "alice"), true)
	granted, err := tb.b.Status(grantedCtx, &localpb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status with read access: %v", err)
	}
	if len(resp.Peers) != len(granted.Peers) {
		t.Fatalf("Status returned %d peers without read access, want the %d returned with read access", len(resp.Peers), len(granted.Peers))
	}
	for i := range resp.Peers {
		if !proto.Equal(resp.Peers[i], granted.Peers[i]) {
			t.Fatalf("Status peer %d without read access = %v, want %v", i, resp.Peers[i], granted.Peers[i])
		}
	}
}

// TestStatusWithoutCallerIdentity checks that Status does not fall back to
// filtering by UID 0 when the context carries no caller identity: without a
// caller it returns no agents, not the agents owned by UID 0, while the peer
// list stays visible as it does for any caller without read permission.
func TestStatusWithoutCallerIdentity(t *testing.T) {
	// REQ-broker-config-010
	tb := newTestBroker(t)
	root, _ := statusRegisterAgent(t, tb, "root")

	tb.b.mu.Lock()
	tb.b.agents[root].caller.UID = 0
	tb.b.mu.Unlock()

	tb.peers.infos = []*localpb.BrokerInfo{{Alias: "hostB", IsPeer: true}}

	ctx := withReadGranted(context.Background(), false)
	resp, err := tb.b.Status(ctx, &localpb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("Status returned %d agents without caller identity, want 0: %v", len(resp.Agents), resp.Agents)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("Status returned %d peers without caller identity, want 1: %v", len(resp.Peers), resp.Peers)
	}
}

func TestStatusWithReadAccess(t *testing.T) {
	tb := newTestBroker(t)
	mine, other := statusAccessAgents(t, tb)

	ctx := withReadGranted(accessCallerContext(1000, "alice"), true)
	resp, err := tb.b.Status(ctx, &localpb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Agents) != 2 {
		t.Fatalf("Status returned %d agents, want 2: %v", len(resp.Agents), resp.Agents)
	}
	seen := map[string]bool{}
	for _, entry := range resp.Agents {
		seen[entry.AgentId] = true
	}
	if !seen[string(mine)] || !seen[string(other)] {
		t.Fatalf("Status agents = %v, want %s and %s", resp.Agents, mine, other)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("Status returned %d peers with read access, want 1", len(resp.Peers))
	}
}
