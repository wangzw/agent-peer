//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker"
	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// The size-limit case is defined against the product's own computation. A
// signature change breaks this test at compile time, which is the intent of the
// plan's assertion.
var _ func(name, ref, text string) int = broker.MaxSendFrameSize

const (
	// srTopic is the topic the two sessions share.
	srTopic = "t"

	// srSendBudget is send.request_timeout (2s) plus the 500ms allowance the
	// plan puts on one send in these scenarios.
	srSendBudget = 2500 * time.Millisecond

	// srReplyBudget is the five-second send budget of the reply-loss case, plus
	// the same 500ms allowance.
	srReplyBudget = 5500 * time.Millisecond

	// srRelinkBound is reconnect_max (400ms) + dial_timeout (500ms) + 3s.
	srRelinkBound = 3900 * time.Millisecond

	// srWaitBound bounds a wait that no product timer governs.
	srWaitBound = 3 * time.Second

	// srQuietWindow is send.request_timeout: after the link is back, a message
	// that was not delivered must not surface within the sender's own budget.
	// It is a deliberate observation, as the plan requires for negative checks.
	srQuietWindow = 2 * time.Second

	// srFrameLimit is the encoded-frame limit of one inbox message.
	srFrameLimit = 1_000_000
)

// TestRemoteSendFaults covers cross-host sending under a NAT: both directions
// behind the router, an unreachable peer, the guarantee that a failed send is
// not delivered later, an unknown result after the reply is lost, the reply-loss
// duplicate, and the size limit. A is the acceptor and B the dialer.
func TestRemoteSendFaults(t *testing.T) {
	c := harness.Up(t, harness.TopoNAT)
	aNode, bNode := c.Node("hostA"), c.Node("hostB")
	srStartBroker(t, aNode, srConfig())
	srStartBroker(t, bNode, srConfig())
	opA, opB := srOperator(t, aNode), srOperator(t, bNode)

	a1 := srSession(t, aNode, "a1")
	b1 := srSession(t, bNode, "b1")
	defer a1.End(t)
	defer b1.End(t)

	key := srCreateKey(t, opA)
	srConnect(t, opB, srShownAddress(t, c, "hostA", key.Addresses), key.Key).MustOK(t)
	srWaitConnected(t, bNode, "hostA-17443", srRelinkBound)
	srWaitConnected(t, aNode, "hostB-17443", srRelinkBound)

	t.Run("REQ-broker-pairing-020 both directions behind NAT", func(t *testing.T) {
		fromA := srMark("nat-a-")
		r := a1.Call(t, "send_message", map[string]any{"to": "b1@hostB-17443", "message": fromA})
		r.MustOK(t)
		frame := b1.WaitFrame(t, srWaitBound)
		if body := srEnvelopeBody(t, frame.Content); body != fromA {
			t.Fatalf("B received %q, want %q", body, fromA)
		}

		fromB := srMark("nat-b-")
		r = b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": fromB})
		r.MustOK(t)
		frame = a1.WaitFrame(t, srWaitBound)
		if body := srEnvelopeBody(t, frame.Content); body != fromB {
			t.Fatalf("A received %q, want %q", body, fromB)
		}
	})

	// The two bodies below must never surface after the cut is restored; the
	// next two cases check both that they do not arrive before the sentinel and
	// that they do not arrive in the sender's whole budget afterwards.
	var failedFromA, failedFromB string

	t.Run("REQ-agent-messaging-017 unreachable within budget", func(t *testing.T) {
		srCutAndWaitDown(t, c, aNode, bNode)

		// The dialer side stops after its retries are spent; the acceptor side
		// waits out the rest of the budget. Both must answer inside the budget.
		failedFromB = srMark("unreachable-b-")
		start := time.Now()
		r := b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": failedFromB})
		elapsed := time.Since(start)
		r.MustCode(t, "peer_unreachable")
		if !r.HasPrefix("Not sent") {
			t.Fatalf("the dialer's error is %q, want it to start with Not sent", r.Text)
		}
		if elapsed > srSendBudget {
			t.Fatalf("the dialer's send returned after %s, want at most %s", elapsed, srSendBudget)
		}

		failedFromA = srMark("unreachable-a-")
		start = time.Now()
		r = a1.Call(t, "send_message", map[string]any{"to": "b1@hostB-17443", "message": failedFromA})
		elapsed = time.Since(start)
		r.MustCode(t, "peer_unreachable")
		if !r.HasPrefix("Not sent") {
			t.Fatalf("the acceptor's error is %q, want it to start with Not sent", r.Text)
		}
		if elapsed > srSendBudget {
			t.Fatalf("the acceptor's send returned after %s, want at most %s", elapsed, srSendBudget)
		}
	})

	t.Run("REQ-agent-messaging-019 never delivered later", func(t *testing.T) {
		c.Restore(t)
		srWaitConnected(t, bNode, "hostA-17443", srRelinkBound)
		srWaitConnected(t, aNode, "hostB-17443", srRelinkBound)

		// Sentinels prove the direction works again before the old bodies are
		// declared absent: a queued message would have to sit in front of them.
		sentinelToB := srMark("sentinel-to-b-")
		r := a1.Call(t, "send_message", map[string]any{"to": "b1@hostB-17443", "message": sentinelToB})
		r.MustOK(t)
		b1.WaitFrame(t, srWaitBound)

		sentinelToA := srMark("sentinel-to-a-")
		r = b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": sentinelToA})
		r.MustOK(t)
		a1.WaitFrame(t, srWaitBound)

		if frames := b1.Fake().Frames(); srCountMark(frames, failedFromA) != 0 || srCountMark(frames, failedFromB) != 0 {
			t.Fatalf("B received a message that was reported Not sent")
		}
		if frames := a1.Fake().Frames(); srCountMark(frames, failedFromA) != 0 || srCountMark(frames, failedFromB) != 0 {
			t.Fatalf("A received a message that was reported Not sent")
		}

		baseA, baseB := len(a1.Fake().Frames()), len(b1.Fake().Frames())
		harness.Consistently(t, srQuietWindow, "the failed messages never arrive", func() (bool, string) {
			framesA, framesB := a1.Fake().Frames(), b1.Fake().Frames()
			if len(framesA) != baseA || len(framesB) != baseB {
				return false, fmt.Sprintf("frame counts grew from A=%d B=%d to A=%d B=%d", baseA, baseB, len(framesA), len(framesB))
			}
			if srCountMark(framesA, failedFromA) != 0 || srCountMark(framesB, failedFromB) != 0 {
				return false, "a message reported Not sent was delivered"
			}
			return true, ""
		})
	})

	t.Run("REQ-agent-messaging-027 delivery unknown", func(t *testing.T) {
		// HoldOpen makes the receiver's Broker block while writing, so the frame
		// being recorded proves the request is in flight. Cutting the link then
		// loses the reply while the write itself succeeds.
		a1.Fake().SetMode(fakesession.HoldOpen)
		marker := srMark("unknown-")
		ch := srSendAsync(t, b1, "a1@hostA-17443", marker)
		frame := a1.WaitFrame(t, srWaitBound)
		if !strings.Contains(frame.Content, marker) {
			t.Fatalf("the in-flight frame carries %q, want the marker %q", frame.Content, marker)
		}

		c.Cut(t, "hostA", "hostB")
		a1.Fake().Release()

		r := srAwait(t, ch, srSendBudget, "the delivery-unknown send")
		r.MustCode(t, "delivery_unknown")
		if !r.HasPrefix("Delivery unknown") {
			t.Fatalf("the error is %q, want it to start with Delivery unknown", r.Text)
		}

		c.Restore(t)
		if got := srCountMark(a1.Fake().Frames(), marker); got != 1 {
			t.Fatalf("A received the message %d times, want exactly once", got)
		}
		srWaitConnected(t, bNode, "hostA-17443", srRelinkBound)
		srWaitConnected(t, aNode, "hostB-17443", srRelinkBound)
		a1.Fake().SetMode(fakesession.CloseAfterRead)
	})

	t.Run("REQ-agent-messaging-020 reply lost, one copy", func(t *testing.T) {
		// The sender's budget decides this case, so B restarts with five seconds.
		// The config file is replaced first: an MCP server process may start the
		// Broker before this test does, and it must read the new value.
		config := srConfig("send:\n  request_timeout: 5s\n")
		bNode.WriteConfig(t, config)
		bNode.StopBroker(t, syscall.SIGTERM)
		srStartBroker(t, bNode, config)
		srWaitConnected(t, bNode, "hostA-17443", srRelinkBound)
		srWaitConnected(t, aNode, "hostB-17443", srRelinkBound)
		srWaitSessionVisible(t, a1, "b1")

		a1.Fake().SetMode(fakesession.HoldOpen)
		marker := srMark("reply-lost-")
		ch := srSendAsync(t, b1, "a1@hostA-17443", marker)
		frame := a1.WaitFrame(t, srWaitBound)
		if !strings.Contains(frame.Content, marker) {
			t.Fatalf("the in-flight frame carries %q, want the marker %q", frame.Content, marker)
		}

		c.Cut(t, "hostA", "hostB")
		a1.Fake().Release()
		c.Restore(t)

		r := srAwait(t, ch, srReplyBudget, "the reply-loss send")
		r.MustOK(t)

		// A second message proves the link is usable again. The first message must
		// still exist exactly once: the resend reuses its identifier and the
		// receiver's dedupe table must not write it again.
		a1.Fake().SetMode(fakesession.CloseAfterRead)
		sentinel := srMark("reply-lost-sentinel-")
		r = b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": sentinel})
		r.MustOK(t)
		a1.WaitFrame(t, srWaitBound)

		if got := srCountMark(a1.Fake().Frames(), marker); got != 1 {
			t.Fatalf("A received the reply-loss message %d times, want exactly once", got)
		}
	})

	t.Run("REQ-agent-messaging-026 size limit", func(t *testing.T) {
		// A second session named b1 makes b1's own address carry its ref, which
		// is the value the sender uses when it computes the frame size. The
		// duplicate stays alive for the whole case so the ref does not change
		// back under the sends.
		duplicate := srSession(t, bNode, "b1")
		defer duplicate.End(t)

		var ref string
		harness.Eventually(t, srWaitBound, "b1's address carries its ref", func() (bool, string) {
			out, state := srListAgents(t, b1)
			if state != "" {
				return false, state
			}
			name, gotRef, alias := srSplitAddress(out.Self)
			if name != "b1" || gotRef == "" || alias != "hostB-17443" {
				return false, "self_address is " + out.Self
			}
			ref = gotRef
			return true, ""
		})

		// The body is ASCII, so FrameSize grows by exactly one per byte. Probe
		// the empty body to get the base, then assert both sides of the boundary
		// with the product's function.
		base := broker.MaxSendFrameSize("b1", ref, "")
		atLimit := srFrameLimit - base
		overLimit := atLimit + 1
		if got := broker.MaxSendFrameSize("b1", ref, strings.Repeat("x", atLimit)); got != srFrameLimit {
			t.Fatalf("the computed boundary sizes to %d, want %d", got, srFrameLimit)
		}
		if got := broker.MaxSendFrameSize("b1", ref, strings.Repeat("x", overLimit)); got != srFrameLimit+1 {
			t.Fatalf("one byte past the boundary sizes to %d, want %d", got, srFrameLimit+1)
		}

		bytesIn := c.ByteCounter(t, "hostA", c.Addr("router", "wan"), 17443)
		before := bytesIn()
		framesBefore := len(a1.Fake().Frames())

		oversize := strings.Repeat("x", overLimit)
		r := b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": oversize})
		r.MustCode(t, "too_large")
		if !r.HasPrefix("Not sent") {
			t.Fatalf("the oversize error is %q, want it to start with Not sent", r.Text)
		}
		if grew := bytesIn() - before; grew >= 100_000 {
			t.Fatalf("the rejected message put %d bytes on the link, want far less than the body", grew)
		}
		if got := len(a1.Fake().Frames()); got != framesBefore {
			t.Fatalf("A received %d new frames for a rejected message", got-framesBefore)
		}

		atBoundary := strings.Repeat("x", atLimit)
		r = b1.Call(t, "send_message", map[string]any{"to": "a1@hostA-17443", "message": atBoundary})
		r.MustOK(t)
		frame := a1.WaitFrame(t, srWaitBound)
		if body := srEnvelopeBody(t, frame.Content); body != atBoundary {
			t.Fatalf("the boundary message arrived with %d bytes, want %d", len(body), len(atBoundary))
		}
	})
}

// srConfig merges the shortened timers with the given fragments.
func srConfig(parts ...string) string {
	return harness.ConfigYAML(append([]string{harness.FastTimers}, parts...)...)
}

// srStartBroker starts a Broker and fails when it does not come up. Exit code 3
// means another process started one first; the helper then waits for it.
func srStartBroker(t *testing.T, node *harness.Node, config string) {
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

// srOperator starts a session that never registers as an Agent.
func srOperator(t *testing.T, node *harness.Node) *harness.Session {
	t.Helper()
	s := node.StartSession(t, harness.SessionSpec{Name: "operator", StartMCP: true, NoSocketEnv: true})
	harness.Eventually(t, srWaitBound, "the operator session reaches its Broker", func() (bool, string) {
		r := s.Call(t, "list_brokers", map[string]any{})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
	return s
}

// srSession starts a registered session in srTopic.
func srSession(t *testing.T, node *harness.Node, name string) *harness.Session {
	t.Helper()
	s := node.StartSession(t, harness.SessionSpec{Name: name, StartMCP: true})
	srWaitRegistered(t, s, name)
	srJoin(t, s, srTopic)
	return s
}

// srWaitRegistered waits until the session lists itself under name.
func srWaitRegistered(t *testing.T, s *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, srWaitBound, "session "+name+" is registered", func() (bool, string) {
		out, state := srListAgents(t, s)
		if state != "" {
			return false, state
		}
		if got, _, _ := srSplitAddress(out.Self); got != name {
			return false, "self_address is " + out.Self
		}
		return true, ""
	})
}

// srWaitSessionVisible waits until the session's listing contains name.
func srWaitSessionVisible(t *testing.T, s *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, srRelinkBound+srWaitBound, "the session "+name+" is visible again", func() (bool, string) {
		out, state := srListAgents(t, s)
		if state != "" {
			return false, state
		}
		if _, ok := srFindAgent(out, name); !ok {
			return false, name + " is not listed"
		}
		return true, ""
	})
}

// srJoin joins name, retrying while the session is still registering.
func srJoin(t *testing.T, s *harness.Session, topic string) {
	t.Helper()
	harness.Eventually(t, srWaitBound, "join_topic "+topic, func() (bool, string) {
		r := s.Call(t, "join_topic", map[string]any{"name": topic})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
}

// srCreateKey creates one pairing key.
func srCreateKey(t *testing.T, op *harness.Session) mcpserver.CreatePairingKeyOut {
	t.Helper()
	r := op.Call(t, "create_pairing_key", map[string]any{})
	r.MustOK(t)
	out, err := harness.Decode[mcpserver.CreatePairingKeyOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding create_pairing_key: %v", err)
	}
	return out
}

// srConnect calls connect_broker.
func srConnect(t *testing.T, op *harness.Session, address, key string) harness.Result {
	t.Helper()
	return op.Call(t, "connect_broker", map[string]any{"address": address, "key": key})
}

// srShownAddress picks the shown address of node on the wan network.
func srShownAddress(t *testing.T, c *harness.Cluster, node string, shown []string) string {
	t.Helper()
	want := net.JoinHostPort(c.Addr(node, "wan"), "17443")
	for _, address := range shown {
		if address == want {
			return address
		}
	}
	if len(shown) > 0 {
		return shown[0]
	}
	t.Fatalf("create_pairing_key on %s returned no shown address", node)
	return ""
}

// srWaitConnected waits until node lists alias as connected.
func srWaitConnected(t *testing.T, node *harness.Node, alias string, bound time.Duration) {
	t.Helper()
	harness.Eventually(t, bound, "the link to "+alias+" is connected", func() (bool, string) {
		resp, detail := node.Oracle().ListBrokers(t)
		if detail != nil {
			return false, detail.GetMessage()
		}
		for _, broker := range resp.GetBrokers() {
			if broker.GetAlias() == alias {
				return broker.GetStatus() == localpb.BrokerStatus_BROKER_STATUS_CONNECTED, fmt.Sprintf("status is %s", broker.GetStatus())
			}
		}
		return false, "the peer is not listed"
	})
}

// srCutAndWaitDown cuts the network and waits until neither side reports the
// link as connected.
func srCutAndWaitDown(t *testing.T, c *harness.Cluster, aNode, bNode *harness.Node) {
	t.Helper()
	c.Cut(t, "hostA", "hostB")
	harness.Eventually(t, srWaitBound, "both sides notice the cut", func() (bool, string) {
		if srPeerConnected(t, aNode, "hostB-17443") || srPeerConnected(t, bNode, "hostA-17443") {
			return false, "a side still reports the link connected"
		}
		return true, ""
	})
}

// srPeerConnected reports whether node lists alias as connected.
func srPeerConnected(t *testing.T, node *harness.Node, alias string) bool {
	t.Helper()
	resp, detail := node.Oracle().ListBrokers(t)
	if detail != nil {
		return false
	}
	for _, broker := range resp.GetBrokers() {
		if broker.GetAlias() == alias {
			return broker.GetStatus() == localpb.BrokerStatus_BROKER_STATUS_CONNECTED
		}
	}
	return false
}

// srListAgents calls list_agents and returns the decoded answer, with a tool
// error reported as a state string.
func srListAgents(t *testing.T, s *harness.Session) (mcpserver.ListAgentsOut, string) {
	t.Helper()
	r := s.Call(t, "list_agents", nil)
	if r.IsError {
		return mcpserver.ListAgentsOut{}, r.Text
	}
	out, err := harness.Decode[mcpserver.ListAgentsOut](string(r.Structured))
	if err != nil {
		return mcpserver.ListAgentsOut{}, "decoding list_agents: " + err.Error()
	}
	return out, ""
}

// srFindAgent returns the entry whose name part is name.
func srFindAgent(out mcpserver.ListAgentsOut, name string) (mcpserver.AgentOut, bool) {
	for _, agent := range out.Agents {
		if got, _, _ := srSplitAddress(agent.Address); got == name {
			return agent, true
		}
	}
	return mcpserver.AgentOut{}, false
}

// srSplitAddress splits "name [ref]@alias" into its three parts. ref is empty
// when the address carries none.
func srSplitAddress(address string) (name, ref, alias string) {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return strings.TrimSpace(address), "", ""
	}
	name, alias = address[:at], address[at+1:]
	if open := strings.LastIndexByte(name, '['); open >= 0 && strings.HasSuffix(name, "]") {
		ref = name[open+1 : len(name)-1]
		name = name[:open]
	}
	return strings.TrimSpace(name), strings.TrimSpace(ref), alias
}

// srEnvelopeBody returns the message body from the attribution envelope the
// transport wraps it in.
func srEnvelopeBody(t *testing.T, content string) string {
	t.Helper()
	openEnd := strings.Index(content, ">\n")
	if openEnd < 0 {
		t.Fatalf("the delivered content has no envelope opening line: %.80q", content)
	}
	body := content[openEnd+2:]
	const closing = "\n</cross-session-message>"
	if !strings.HasSuffix(body, closing) {
		t.Fatalf("the delivered content has no envelope closing tag: %.80q", content)
	}
	return strings.TrimSuffix(body, closing)
}

// srCountMark counts the frames whose content carries marker.
func srCountMark(frames []fakesession.Frame, marker string) int {
	n := 0
	for _, frame := range frames {
		if strings.Contains(frame.Content, marker) {
			n++
		}
	}
	return n
}

// srAsyncResult carries one asynchronous tool result.
type srAsyncResult struct {
	res harness.Result
	ok  bool
}

// srSendAsync sends one message in the background. The result channel is
// buffered and closed after the call returns; a closed channel with no value
// means the goroutine ended inside the harness failure path.
func srSendAsync(t *testing.T, s *harness.Session, to, message string) <-chan srAsyncResult {
	t.Helper()
	ch := make(chan srAsyncResult, 1)
	go func() {
		defer close(ch)
		res := s.Call(t, "send_message", map[string]any{"to": to, "message": message})
		ch <- srAsyncResult{res: res, ok: true}
	}()
	return ch
}

// srAwait waits for an asynchronous result and fails the test when it does not
// arrive in bound.
func srAwait(t *testing.T, ch <-chan srAsyncResult, bound time.Duration, what string) harness.Result {
	t.Helper()
	select {
	case result, open := <-ch:
		if !open || !result.ok {
			t.Fatalf("%s: the asynchronous call ended without a result", what)
		}
		return result.res
	case <-time.After(bound):
		t.Fatalf("%s: no result within %s", what, bound)
		return harness.Result{}
	}
}

// srMark returns a unique ASCII marker that cannot collide with envelope text.
func srMark(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("send_remote_test: reading random bytes: %v", err))
	}
	return prefix + hex.EncodeToString(b[:])
}
