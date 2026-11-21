package broker

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/peers/internal/broker/deliverpb"
	"github.com/wangzw/peers/internal/link"
	"github.com/wangzw/peers/internal/pairing"
	"github.com/wangzw/peers/internal/testkit/fakesession"
)

// loopbackFixture wires two deliveries, a sender and a receiver, through one
// loopbackLink. The receiver's kind-400 handler is registered on the link, so a
// send crosses a real gRPC stream before it reaches the simulated session.
type loopbackFixture struct {
	t        *testing.T
	link     *loopbackLink
	sender   *delivery
	receiver *delivery
	caller   agentView
	target   agentView
	session  *fakesession.Fake
}

// loopbackSettings returns send settings small enough for tests but with a
// budget that covers a 1 MB gRPC round trip.
func loopbackSettings(alias string) *fakeSettings {
	return newFakeSettings(sendSettings{
		RequestTimeout: 3 * time.Second,
		LinkBackoff:    5 * time.Millisecond,
		InboxBackoff:   5 * time.Millisecond,
		InboxTimeout:   2 * time.Second,
		DedupeWindow:   time.Minute,
		LinkRetries:    3,
		InboxRetries:   2,
	}, alias)
}

// quietLogger returns a logger that keeps its output out of the test log.
func quietLogger() *slog.Logger {
	var level slog.LevelVar
	return NewLogger(&bytes.Buffer{}, &level)
}

// newLoopbackFixture builds the two ends. The sender is the dialer towards the
// receiver, with the caller's roster entry pinned to the receiver's target.
func newLoopbackFixture(t *testing.T) *loopbackFixture {
	t.Helper()

	senderID, receiverID := fakenetPeer(1), fakenetPeer(2)
	link := newLoopbackLink(t, senderID)

	caller := testAgent("api-worker", t.TempDir()+"/api-worker.sock")
	session := fakesession.New(t, fakesession.Options{Name: "frontend", Mode: fakesession.CloseAfterRead})
	target := liveTestAgent(t, session, "frontend")

	senderTopics := &deliveryTopics{
		local: map[string][]string{caller.ID: {"t1"}},
		remote: map[peerID][]deliveryRemoteEntry{
			receiverID: {{Member: remoteMember{Name: target.Name, Ref: target.Ref}, Topics: []string{"t1"}}},
		},
	}
	senderPeers := &deliveryPeers{
		aliases:   map[string]peerID{"hostB": receiverID},
		roles:     map[peerID]pairing.Role{receiverID: pairing.RoleDialer},
		effective: map[peerID]string{receiverID: "hostB"},
	}
	sender := newDelivery(deliveryDeps{
		Agents:   newFakeAgents(caller),
		Topics:   senderTopics,
		Peers:    senderPeers,
		Link:     link,
		Settings: loopbackSettings("hostA"),
		Log:      quietLogger(),
	})

	receiverTopics := &deliveryTopics{
		local: map[string][]string{target.ID: {"t1"}},
		remote: map[peerID][]deliveryRemoteEntry{
			senderID: {{Member: remoteMember{Name: caller.Name, Ref: caller.Ref}, Topics: []string{"t1"}}},
		},
	}
	receiverPeers := &deliveryPeers{
		aliases:   make(map[string]peerID),
		roles:     map[peerID]pairing.Role{senderID: pairing.RoleAcceptor},
		effective: map[peerID]string{senderID: "hostA"},
	}
	receiver := newDelivery(deliveryDeps{
		Agents:   newFakeAgents(target),
		Topics:   receiverTopics,
		Peers:    receiverPeers,
		Link:     link,
		Settings: loopbackSettings("hostB"),
		Log:      quietLogger(),
	})
	receiver.register()

	return &loopbackFixture{
		t:        t,
		link:     link,
		sender:   sender,
		receiver: receiver,
		caller:   caller,
		target:   target,
		session:  session,
	}
}

// send runs one send from the caller to frontend@hostB.
func (f *loopbackFixture) send(text string) error {
	f.t.Helper()
	return f.sender.Send(context.Background(), f.caller, "frontend@hostB", text)
}

// TestLoopbackDeliver checks a message reaches the simulated session across a
// real gRPC stream, with the sender's address as the from-name
// (REQ-agent-messaging-013, REQ-agent-messaging-022, REQ-agent-messaging-015).
func TestLoopbackDeliver(t *testing.T) {
	f := newLoopbackFixture(t)

	if err := f.send("hello over gRPC"); err != nil {
		t.Fatalf("Send = %v, want nil", err)
	}

	// The frame must already be recorded when Send returns: the reply is sent
	// only after inbox.Deliver returns.
	frames := f.session.Frames()
	if len(frames) != 1 {
		t.Fatalf("session recorded %d frames, want 1", len(frames))
	}
	if frames[0].FromName != "api-worker@hostA" {
		t.Fatalf("FromName = %q, want %q: the signature is the sender's address on the receiver",
			frames[0].FromName, "api-worker@hostA")
	}
	if !strings.Contains(frames[0].Content, "hello over gRPC") {
		t.Fatalf("content = %q, want it to contain the message", frames[0].Content)
	}
}

// TestLoopbackMaxSizeMessage checks that a message whose longest-signature
// frame is exactly the 1 000 000-byte limit survives the real gRPC path
// (REQ-agent-messaging-026): the limit must not sit above what the transport
// can carry.
func TestLoopbackMaxSizeMessage(t *testing.T) {
	f := newLoopbackFixture(t)

	text := sendTextAtLimit(t, f.caller.Name, f.caller.Ref)
	if err := f.send(text); err != nil {
		t.Fatalf("Send at the limit = %v, want nil", err)
	}

	frames := f.session.Frames()
	if len(frames) != 1 {
		t.Fatalf("session recorded %d frames, want 1", len(frames))
	}
	if !strings.Contains(frames[0].Content, text) {
		t.Fatalf("content of %d bytes does not contain the full %d-byte message",
			len(frames[0].Content), len(text))
	}
}

// TestLoopbackResendAfterStreamClose checks that a request whose reply is lost
// with the stream is resent under the same message id after the stream is
// reopened, and that the receiver still writes one frame
// (REQ-agent-messaging-020).
func TestLoopbackResendAfterStreamClose(t *testing.T) {
	f := newLoopbackFixture(t)

	// The receiver writes the message and closes the stream before its reply
	// can leave, so the sender sees a sent request with no reply.
	var mu sync.Mutex
	var ids []string
	var calls int
	f.link.HandleRequest(link.FrameKind(deliverpb.KindDeliver),
		func(ctx context.Context, p peerID, fr link.Frame) link.Frame {
			req, err := decodeDeliverRequest(fr)
			if err == nil {
				mu.Lock()
				ids = append(ids, req.MessageID)
				mu.Unlock()
			}
			reply := f.receiver.handleDeliver(ctx, p, fr)
			mu.Lock()
			calls++
			first := calls == 1
			mu.Unlock()
			if first {
				f.link.closeStream()
			}
			return reply
		})

	if err := f.send("resend me"); err != nil {
		t.Fatalf("Send = %v, want nil after the resend", err)
	}

	mu.Lock()
	gotIDs, gotCalls := append([]string(nil), ids...), calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("receiver handled %d requests, want 2: the first must be resent", gotCalls)
	}
	if len(gotIDs) != 2 {
		t.Fatalf("decoded %d message ids, want 2", len(gotIDs))
	}
	if gotIDs[0] != gotIDs[1] {
		t.Fatalf("message ids after the resend = %q and %q, want the same id", gotIDs[0], gotIDs[1])
	}

	frames := f.session.Frames()
	if len(frames) != 1 {
		t.Fatalf("session recorded %d frames, want 1: the resend must hit the dedupe table", len(frames))
	}
	if frames[0].MsgID != gotIDs[0] {
		t.Fatalf("frame msg_id = %q, want the request id %q", frames[0].MsgID, gotIDs[0])
	}
}
