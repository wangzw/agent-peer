//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/test/e2e/ctl"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

const (
	// lkRelinkBound is reconnect_max (400ms) + dial_timeout (500ms) + 3s.
	lkRelinkBound = 3900 * time.Millisecond
	// lkWaitBound is the bound of a wait that no product timer governs.
	lkWaitBound = 3 * time.Second
	// lkNoRedialWindow is five reconnect intervals (5 * 400ms). A Broker that
	// keeps redialing makes at least four attempts inside it, while a stopped
	// one makes none; the window is a deliberate observation, not a sleep.
	lkNoRedialWindow = 2000 * time.Millisecond
)

// lkTopic is the topic the sessions use.
const lkTopic = "t"

// TestLinks covers link survival, redial after a cut, the acceptor never
// dialing, restart recovery, address-list changes, unpairing and the Broker
// states reported by list_brokers. A is the acceptor behind no NAT and B is the
// dialer behind the router.
func TestLinks(t *testing.T) {
	c := harness.Up(t, harness.TopoNAT)
	aNode, bNode := c.Node("hostA"), c.Node("hostB")
	lkStartBroker(t, aNode, lkConfig())
	lkStartBroker(t, bNode, lkConfig())
	opA, opB := lkOperator(t, aNode), lkOperator(t, bNode)

	// The counter is installed before pairing so any dial A ever makes is
	// counted, including one made while the link is being set up.
	outSYN := c.SYNCounter(t, "hostA", harness.Outbound, "", 17443)

	a1 := lkSession(t, aNode, "a1")
	b1 := lkSession(t, bNode, "b1")
	var (
		aSawPeerB bool
		a1Ended   bool
		b1Ended   bool
	)
	defer func() {
		if !a1Ended {
			a1.End(t)
		}
		if !b1Ended {
			b1.End(t)
		}
	}()

	addrA := lkAddr(c, "hostA", "wan")
	key := lkCreateKey(t, opA)
	lkConnect(t, opB, addrA, key.Key).MustOK(t)
	lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound)
	lkWaitConnected(t, aNode, "hostB-17443", lkRelinkBound)

	t.Run("REQ-broker-pairing-016 relink without key", func(t *testing.T) {
		lkCutAndWaitDown(t, c, aNode, bNode)
		c.Restore(t)
		lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound)
		lkWaitConnected(t, aNode, "hostB-17443", lkRelinkBound)

		// No connect_broker was called during the outage; the consumed
		// one-time key must not work a second time.
		r := lkConnect(t, opB, addrA, key.Key)
		r.MustCode(t, "pairing_rejected")
		if !r.HasPrefix("Not connected") {
			t.Fatalf("the reused key error is %q, want it to start with Not connected", r.Text)
		}
	})

	t.Run("REQ-broker-pairing-018 redial after restore", func(t *testing.T) {
		lkCutAndWaitDown(t, c, aNode, bNode)
		start := time.Now()
		c.Restore(t)
		lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound)
		lkWaitConnected(t, aNode, "hostB-17443", lkRelinkBound)
		if elapsed := time.Since(start); elapsed > lkRelinkBound {
			t.Fatalf("the link took %s to rebuild, want at most %s", elapsed, lkRelinkBound)
		}
	})

	t.Run("REQ-broker-pairing-027 acceptor never dials", func(t *testing.T) {
		// The two cut/restore cycles above must already show A never dialing.
		if got := outSYN(); got != 0 {
			t.Fatalf("A sent %d outbound SYNs while it was only the acceptor", got)
		}

		// Block B's path to A's only advertised address; then a cut and restore
		// leave B unable to redial. A must not dial in the other direction.
		undo := c.Block(t, "hostB", c.Addr("hostA", "wan"))
		t.Cleanup(undo)
		lkCutAndWaitDown(t, c, aNode, bNode)
		c.Restore(t)

		harness.Consistently(t, lkNoRedialWindow, "the link stays down and A never dials", func() (bool, string) {
			if got := outSYN(); got != 0 {
				return false, fmt.Sprintf("A sent %d outbound SYNs", got)
			}
			if lkStatus(t, bNode, "hostA-17443") == localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
				return false, "B considers the link connected although its path is blocked"
			}
			return true, ""
		})
		undo()
		lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound)
	})

	t.Run("REQ-broker-config-014 broker outlives last session", func(t *testing.T) {
		a1.End(t)
		a1Ended = true
		b1.End(t)
		b1Ended = true

		if got := lkBrokerProcesses(t, c, "hostA"); got < 1 {
			t.Fatalf("A has %d Broker processes after both sessions ended", got)
		}
		harness.Consistently(t, time.Second, "B still sees A connected with no sessions left", func() (bool, string) {
			if lkStatus(t, bNode, "hostA-17443") != localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
				return false, "the peer is no longer connected"
			}
			return true, ""
		})
	})

	t.Run("REQ-broker-pairing-017 survives restart", func(t *testing.T) {
		lkRestart(t, c, "hostA", lkConfig(), opA)
		opA = lkOperator(t, aNode)

		// The peer table is loaded before the Broker answers.
		if _, ok := lkFindPeer(lkPeers(t, aNode), "hostB-17443"); !ok {
			t.Fatal("A does not list B immediately after the restart")
		}
		lkWaitConnected(t, aNode, "hostB-17443", lkRelinkBound)

		// Restore the sessions the later cases use.
		a1 = lkSession(t, aNode, "a1")
		b1 = lkSession(t, bNode, "b1")
		a1Ended, b1Ended = false, false
	})

	t.Run("REQ-broker-discovery-012 peers listed", func(t *testing.T) {
		peersB := lkPeers(t, bNode)
		if len(peersB) != 1 {
			t.Fatalf("B lists %d brokers, want one", len(peersB))
		}
		peer := peersB[0]
		if peer.GetAlias() != "hostA-17443" || !peer.GetIsPeer() {
			t.Fatalf("B lists %q (is_peer=%v), want the peer hostA-17443", peer.GetAlias(), peer.GetIsPeer())
		}
		if got := hex.EncodeToString(peer.GetFingerprint()); len(got) != 64 || !lkLowerHex(got) {
			t.Fatalf("B sees A's fingerprint as %q, want 64 lowercase hex digits", got)
		}
		if peer.GetRole() != localpb.PeerRole_PEER_ROLE_DIALER {
			t.Fatalf("B's role is %s, want DIALER", peer.GetRole())
		}
		want := net.JoinHostPort(c.Addr("hostA", "wan"), "17443")
		addrs := peer.GetAdvertisedAddresses()
		if len(addrs) != 1 || addrs[0].GetKind() != "direct" || addrs[0].GetAddr() != want {
			t.Fatalf("B sees A's addresses as %v, want [direct %s]", lkAddressStrings(addrs), want)
		}

		peersA := lkPeers(t, aNode)
		if len(peersA) != 1 {
			t.Fatalf("A lists %d brokers, want one", len(peersA))
		}
		peerB := peersA[0]
		if peerB.GetAlias() != "hostB-17443" || !peerB.GetIsPeer() {
			t.Fatalf("A lists %q (is_peer=%v), want the peer hostB-17443", peerB.GetAlias(), peerB.GetIsPeer())
		}
		if peerB.GetRole() != localpb.PeerRole_PEER_ROLE_ACCEPTOR {
			t.Fatalf("A's role is %s, want ACCEPTOR", peerB.GetRole())
		}
		if len(peerB.GetAdvertisedAddresses()) != 0 {
			t.Fatalf("A invents addresses for the dialer: %v", lkAddressStrings(peerB.GetAdvertisedAddresses()))
		}
		aSawPeerB = true
	})

	t.Run("REQ-broker-discovery-013 three statuses", func(t *testing.T) {
		c.Cut(t, "hostA", "hostB")
		harness.Eventually(t, lkWaitBound, "the two sides report the cut", func() (bool, string) {
			if got := lkStatus(t, bNode, "hostA-17443"); got != localpb.BrokerStatus_BROKER_STATUS_CONNECTABLE {
				return false, fmt.Sprintf("B sees A as %s, want CONNECTABLE", got)
			}
			if got := lkStatus(t, aNode, "hostB-17443"); got != localpb.BrokerStatus_BROKER_STATUS_NOT_DIRECTLY_REACHABLE {
				return false, fmt.Sprintf("A sees B as %s, want NOT_DIRECTLY_REACHABLE", got)
			}
			return true, ""
		})
		if len(lkPeers(t, aNode)) != 1 || len(lkPeers(t, bNode)) != 1 {
			t.Fatal("a peer is listed more than once or missing while the network is cut")
		}

		c.Restore(t)
		lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound)
		lkWaitConnected(t, aNode, "hostB-17443", lkRelinkBound)
	})

	t.Run("REQ-broker-discovery-014 peer flag", func(t *testing.T) {
		// aSawPeerB records the state before the disconnect below; the flag
		// must have been true while the pair existed.
		if !aSawPeerB {
			t.Fatal("A did not mark B as a peer before the disconnect")
		}
	})

	t.Run("REQ-broker-pairing-021 disconnect", func(t *testing.T) {
		r := opA.Call(t, "disconnect_broker", map[string]any{"broker": "hostB-17443"})
		r.MustOK(t)

		harness.Eventually(t, 3*time.Second, "B no longer sees A as connected", func() (bool, string) {
			got := lkStatus(t, bNode, "hostA-17443")
			return got != localpb.BrokerStatus_BROKER_STATUS_CONNECTED, fmt.Sprintf("B sees A as %s", got)
		})
		harness.Eventually(t, 3*time.Second, "A's connection on 17443 is gone", func() (bool, string) {
			if got := establishedOn(t, c, "hostA", 17443); got != 0 {
				return false, fmt.Sprintf("A still has %d established connections on 17443", got)
			}
			return true, ""
		})

		harness.Eventually(t, lkWaitBound, "A forgets B", func() (bool, string) {
			if _, ok := lkFindPeer(lkPeers(t, aNode), "hostB-17443"); ok {
				return false, "A still lists B"
			}
			return true, ""
		})
		harness.Eventually(t, lkWaitBound, "B's session disappears from A's list", func() (bool, string) {
			if lkHasAgent(lkListAgents(t, a1), "b1") {
				return false, "A still lists b1"
			}
			return true, ""
		})
	})

	t.Run("REQ-broker-pairing-035 rejected dialer stops", func(t *testing.T) {
		harness.Eventually(t, lkWaitBound, "B marks the link to A as rejected", func() (bool, string) {
			peer, ok := lkFindPeer(lkPeers(t, bNode), "hostA-17443")
			if !ok {
				return false, "A is not listed"
			}
			if peer.GetLinkState() != localpb.LinkState_LINK_STATE_REJECTED {
				return false, fmt.Sprintf("link_state is %s", peer.GetLinkState())
			}
			if !strings.Contains(peer.GetStatusDetail(), "unpaired") {
				return false, fmt.Sprintf("status_detail is %q", peer.GetStatusDetail())
			}
			return true, ""
		})

		inSYN := c.SYNCounter(t, "hostA", harness.Inbound, c.Addr("router", "wan"), 17443)
		before := inSYN()
		harness.Consistently(t, lkNoRedialWindow, "B stops dialing A", func() (bool, string) {
			if got := inSYN(); got != before {
				return false, fmt.Sprintf("A received %d more SYNs from the router", got-before)
			}
			return true, ""
		})
		// "No dial for ten minutes" is covered by
		// internal/link/dial_test.go's
		// TestREQBrokerPairing035_RejectedStopsRedialing; the shortened window
		// here observes the same stop in the real process.
	})

	t.Run("REQ-broker-pairing-022 re-pair after unpair", func(t *testing.T) {
		fresh := lkCreateKey(t, opA)
		lkConnect(t, opB, addrA, fresh.Key).MustOK(t)
		lkWaitConnected(t, bNode, "hostA-17443", lkWaitBound)
		lkWaitConnected(t, aNode, "hostB-17443", lkWaitBound)
	})

	t.Run("REQ-broker-pairing-034 acceptor addresses change", func(t *testing.T) {
		extra := c.Addr("hostA", "wan2")
		aNode.WriteConfig(t, lkConfig("broker:\n  advertise_extra: ["+extra+"]\n"))

		wan := net.JoinHostPort(c.Addr("hostA", "wan"), "17443")
		wan2 := net.JoinHostPort(extra, "17443")
		harness.Eventually(t, lkWaitBound, "B sees both of A's addresses", func() (bool, string) {
			peer, ok := lkFindPeer(lkPeers(t, bNode), "hostA-17443")
			if !ok {
				return false, "A is not listed"
			}
			addrs := peer.GetAdvertisedAddresses()
			if len(addrs) != 2 || addrs[0].GetAddr() != wan || addrs[1].GetAddr() != wan2 {
				return false, fmt.Sprintf("addresses are %v, want [%s %s]", lkAddressStrings(addrs), wan, wan2)
			}
			return true, ""
		})

		// Block the first address; after the cut and restore B must find the
		// second one, which shows up as an inbound SYN to A from B.
		inSYN := c.SYNCounter(t, "hostA", harness.Inbound, c.Addr("hostB", "lanb"), 17443)
		before := inSYN()
		undo := c.Block(t, "hostB", c.Addr("hostA", "wan"))
		t.Cleanup(undo)
		lkCutAndWaitDown(t, c, aNode, bNode)
		c.Restore(t)
		lkWaitConnected(t, bNode, "hostA-17443", lkRelinkBound+time.Second)
		if got := inSYN(); got <= before {
			t.Fatalf("B never dialed A's second address: %d SYNs, want more than %d", got, before)
		}
		undo()
	})
}

// TestLinksRestartWithTwoPeers restarts a Broker that is the dialer for one
// peer and the acceptor for another; both links must come back.
func TestLinksRestartWithTwoPeers(t *testing.T) {
	c := harness.Up(t, harness.TopoFlat(3))
	xNode, yNode, zNode := c.Node("hostA"), c.Node("hostB"), c.Node("hostC")
	for _, node := range []*harness.Node{xNode, yNode, zNode} {
		lkStartBroker(t, node, lkConfig())
	}
	opX, opY, opZ := lkOperator(t, xNode), lkOperator(t, yNode), lkOperator(t, zNode)

	// X dials Y.
	keyY := lkCreateKey(t, opY)
	lkConnect(t, opX, lkShownAddress(t, c, "hostB", opY), keyY.Key).MustOK(t)
	lkWaitConnected(t, yNode, "hostA-17443", lkRelinkBound)

	// Z dials X.
	keyX := lkCreateKey(t, opX)
	lkConnect(t, opZ, lkShownAddress(t, c, "hostA", opX), keyX.Key).MustOK(t)
	lkWaitConnected(t, xNode, "hostC-17443", lkRelinkBound)

	lkRestart(t, c, "hostA", lkConfig(), opX)
	opX = lkOperator(t, xNode)

	harness.Eventually(t, lkRelinkBound, "X restores both links after the restart", func() (bool, string) {
		peers := lkPeers(t, xNode)
		y, okY := lkFindPeer(peers, "hostB-17443")
		z, okZ := lkFindPeer(peers, "hostC-17443")
		if !okY || !okZ {
			return false, fmt.Sprintf("X lists %v", lkPeerAliases(peers))
		}
		if y.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTED ||
			z.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
			return false, fmt.Sprintf("Y=%s Z=%s", y.GetStatus(), z.GetStatus())
		}
		return true, ""
	})
}

// lkConfig merges the shortened timers with the given fragments.
func lkConfig(parts ...string) string {
	return harness.ConfigYAML(append([]string{harness.FastTimers}, parts...)...)
}

// lkAddr is the address of node's Broker on network.
func lkAddr(c *harness.Cluster, node, network string) string {
	return net.JoinHostPort(c.Addr(node, network), "17443")
}

// lkShownAddress picks the shown address of node on the flat network from a
// key created by op.
func lkShownAddress(t *testing.T, c *harness.Cluster, node string, op *harness.Session) string {
	t.Helper()
	want := net.JoinHostPort(c.Addr(node, "net"), "17443")
	key := lkCreateKey(t, op)
	for _, address := range key.Addresses {
		if address == want {
			return address
		}
	}
	if len(key.Addresses) > 0 {
		return key.Addresses[0]
	}
	t.Fatalf("create_pairing_key on %s returned no shown address", node)
	return ""
}

// lkStartBroker starts a Broker and fails when it does not come up. Exit code 3
// means an MCP server process started one first; the helper then waits.
func lkStartBroker(t *testing.T, node *harness.Node, config string) {
	t.Helper()
	start := node.StartBroker(t, harness.BrokerSpec{ConfigYAML: config})
	if start.Running {
		return
	}
	if start.ExitCode != 3 {
		t.Fatalf("the Broker did not start: exit code %d: %s", start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
	harness.Eventually(t, 10*time.Second, "the Broker started by another process answers", func() (bool, string) {
		res := node.Exec(t, []string{"/usr/local/bin/agent-peer", "status"}, nil)
		return res.ExitCode == 0, fmt.Sprintf("agent-peer status exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	})
}

// lkRestart stops the Broker and starts it with config after ending every MCP
// server process passed in stop.
func lkRestart(t *testing.T, c *harness.Cluster, node string, config string, stop ...*harness.Session) {
	t.Helper()
	for _, s := range stop {
		s.End(t)
	}
	n := c.Node(node)
	n.StopBroker(t, syscall.SIGTERM)
	lkStartBroker(t, n, config)
}

// lkOperator starts an operator session that never registers as an agent.
func lkOperator(t *testing.T, node *harness.Node) *harness.Session {
	t.Helper()
	s := node.StartSession(t, harness.SessionSpec{Name: "operator", StartMCP: true, NoSocketEnv: true})
	harness.Eventually(t, lkWaitBound, "the operator session reaches its Broker", func() (bool, string) {
		r := s.Call(t, "list_brokers", map[string]any{})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
	return s
}

// lkSession starts a registered session in lkTopic.
func lkSession(t *testing.T, node *harness.Node, name string) *harness.Session {
	t.Helper()
	s := node.StartSession(t, harness.SessionSpec{Name: name, StartMCP: true})
	registrationWaitRegistered(t, s, name)
	s.Call(t, "join_topic", map[string]any{"name": lkTopic}).MustOK(t)
	return s
}

// lkCreateKey creates one pairing key.
func lkCreateKey(t *testing.T, op *harness.Session) mcpserver.CreatePairingKeyOut {
	t.Helper()
	r := op.Call(t, "create_pairing_key", map[string]any{})
	r.MustOK(t)
	out, err := harness.Decode[mcpserver.CreatePairingKeyOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding create_pairing_key: %v", err)
	}
	return out
}

// lkConnect calls connect_broker.
func lkConnect(t *testing.T, op *harness.Session, address, key string) harness.Result {
	t.Helper()
	return op.Call(t, "connect_broker", map[string]any{"address": address, "key": key})
}

// lkPeers lists node's peers through the local interface probe.
func lkPeers(t *testing.T, node *harness.Node) []*localpb.BrokerInfo {
	t.Helper()
	resp, detail := node.Oracle().ListBrokers(t)
	if detail != nil {
		t.Fatalf("list_brokers: %s", detail.GetMessage())
	}
	return resp.GetBrokers()
}

// lkFindPeer returns the peer with alias.
func lkFindPeer(peers []*localpb.BrokerInfo, alias string) (*localpb.BrokerInfo, bool) {
	for _, peer := range peers {
		if peer.GetAlias() == alias {
			return peer, true
		}
	}
	return nil, false
}

// lkStatus returns the status of alias in node's list, or UNSPECIFIED.
func lkStatus(t *testing.T, node *harness.Node, alias string) localpb.BrokerStatus {
	t.Helper()
	peer, ok := lkFindPeer(lkPeers(t, node), alias)
	if !ok {
		return localpb.BrokerStatus_BROKER_STATUS_UNSPECIFIED
	}
	return peer.GetStatus()
}

// lkWaitConnected waits until node lists alias as connected.
func lkWaitConnected(t *testing.T, node *harness.Node, alias string, bound time.Duration) {
	t.Helper()
	harness.Eventually(t, bound, "the link to "+alias+" is connected", func() (bool, string) {
		return lkStatus(t, node, alias) == localpb.BrokerStatus_BROKER_STATUS_CONNECTED, fmt.Sprintf("status is %s", lkStatus(t, node, alias))
	})
}

// lkCutAndWaitDown cuts the network and waits until neither side lists the
// other as connected.
func lkCutAndWaitDown(t *testing.T, c *harness.Cluster, aNode, bNode *harness.Node) {
	t.Helper()
	c.Cut(t, "hostA", "hostB")
	harness.Eventually(t, lkWaitBound, "both sides notice the cut", func() (bool, string) {
		a := lkStatus(t, aNode, "hostB-17443")
		b := lkStatus(t, bNode, "hostA-17443")
		if a == localpb.BrokerStatus_BROKER_STATUS_CONNECTED || b == localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
			return false, fmt.Sprintf("A sees B as %s, B sees A as %s", a, b)
		}
		return true, ""
	})
}

// lkListAgents calls list_agents.
func lkListAgents(t *testing.T, s *harness.Session) mcpserver.ListAgentsOut {
	t.Helper()
	r := s.Call(t, "list_agents", map[string]any{})
	r.MustOK(t)
	out, err := harness.Decode[mcpserver.ListAgentsOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding list_agents: %v", err)
	}
	return out
}

// lkHasAgent reports whether the listing contains an agent named name.
func lkHasAgent(out mcpserver.ListAgentsOut, name string) bool {
	for _, agent := range out.Agents {
		if lkAgentName(agent.Address) == name {
			return true
		}
	}
	return false
}

// lkAgentName returns the name part of an agent address.
func lkAgentName(address string) string {
	before, _, _ := strings.Cut(address, "@")
	if i := strings.Index(before, " ["); i >= 0 {
		before = before[:i]
	}
	return strings.TrimSpace(before)
}

// lkBrokerProcesses counts the Broker processes of node as the node agent sees
// them. node.ListProcs scans the host's /proc, which is the wrong namespace
// for a container, so the count goes through the control protocol.
func lkBrokerProcesses(t *testing.T, c *harness.Cluster, node string) int {
	t.Helper()
	client := ctl.Dial(filepath.Join(c.CTLDir(t, node), "ctl.sock"))
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), lkWaitBound)
	defer cancel()
	procs, err := client.Procs(ctx, ctl.ProcsRequest{Kind: "broker"})
	if err != nil {
		t.Fatalf("listing the Broker processes of %s: %v", node, err)
	}
	return len(procs)
}

// lkPeerAliases renders peer aliases for failure messages.
func lkPeerAliases(peers []*localpb.BrokerInfo) []string {
	out := make([]string, 0, len(peers))
	for _, peer := range peers {
		out = append(out, peer.GetAlias())
	}
	return out
}

// lkAddressStrings renders advertised addresses for failure messages.
func lkAddressStrings(addrs []*localpb.AdvertisedAddress) []string {
	out := make([]string, 0, len(addrs))
	for _, address := range addrs {
		out = append(out, address.GetKind()+" "+address.GetAddr())
	}
	return out
}

// lkLowerHex reports whether s is a non-empty run of lowercase hex digits.
func lkLowerHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != ""
}
