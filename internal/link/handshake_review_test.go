package link

import (
	"testing"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/link/linkpb"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// TestKnownAcceptorNonHelloFirstFrameClosesSilently checks that a known peer
// whose first frame is not Hello is closed without a Reject and without an
// audit event: the role check refuses it before the unknown-fingerprint path.
func TestKnownAcceptorNonHelloFirstFrameClosesSilently(t *testing.T) {
	h := newHandshakeHarness(t)
	peer := Fingerprint{0xF1}
	hsAddAcceptor(h.peers, peer, "peer")

	c, local, p := h.newConn(t, peer)
	res := h.serve(c, nil)

	hsSend(t, p, &linkpb.Frame{Kind: 200})

	if r := waitHSResult(t, res); r.ok {
		t.Fatal("a known peer's non-Hello first frame was installed")
	}
	if sent := local.sent(); len(sent) != 0 {
		t.Fatalf("the link sent %d frames to a known peer with a bad first frame: %+v", len(sent), sent)
	}
	if evs := h.settings.Events(); len(evs) != 0 {
		t.Fatalf("known peer's non-Hello first frame wrote audit events: %+v", evs)
	}
	hsWaitClosed(t, c)
}

// TestAcceptorMalformedHelloClosesSilently checks that a known peer's Hello
// that fails to decode closes the connection without a Reject or an audit
// event: the decode error is a protocol failure, not a peer-table decision.
func TestAcceptorMalformedHelloClosesSilently(t *testing.T) {
	h := newHandshakeHarness(t)
	peer := Fingerprint{0xF2}
	hsAddAcceptor(h.peers, peer, "peer")

	c, local, p := h.newConn(t, peer)
	res := h.serve(c, nil)

	// A 31-byte nonce is a valid protobuf Hello but a malformed handshake.
	hsSend(t, p, encodeHello(linkpb.ProtocolVersion, "peer", make([]byte, nonceSize-1), nil))

	if r := waitHSResult(t, res); r.ok {
		t.Fatal("a malformed Hello was installed")
	}
	if sent := local.sent(); len(sent) != 0 {
		t.Fatalf("the link answered a malformed Hello with %d frames: %+v", len(sent), sent)
	}
	if evs := h.settings.Events(); len(evs) != 0 {
		t.Fatalf("malformed Hello wrote audit events: %+v", evs)
	}
	hsWaitClosed(t, c)
}

// TestUnknownFingerprintMalformedHelloIsAudited checks that an unknown peer is
// recognised before its Hello is decoded, so a malformed Hello still produces
// the "not a peer" Reject and a connection_rejected audit event.
func TestUnknownFingerprintMalformedHelloIsAudited(t *testing.T) {
	h := newHandshakeHarness(t)
	peer := Fingerprint{0xF3}

	c, _, p := h.newConn(t, peer)
	res := h.serve(c, nil)

	hsSend(t, p, encodeHello(linkpb.ProtocolVersion, "stranger", make([]byte, nonceSize-1), nil))

	rej := hsRecv(t, p)
	if rej.GetKind() != linkpb.KindReject {
		t.Fatalf("first reply kind = %d, want reject", rej.GetKind())
	}
	reason, err := decodeReject(rej)
	if err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if reason != linkpb.RejectReason_REJECT_REASON_NOT_A_PEER {
		t.Fatalf("reject reason = %v, want not a peer", reason)
	}

	if r := waitHSResult(t, res); r.ok {
		t.Fatal("unknown fingerprint with a malformed Hello was installed")
	}
	ev := h.settings.WaitEvent(t, config.EventConnectionRejected)
	if ev.Peer == nil || ev.Peer.Fingerprint != [32]byte(peer) {
		t.Fatalf("audit Peer = %+v, want fingerprint %x", ev.Peer, peer)
	}
	if evs := h.settings.Events(); len(evs) != 1 {
		t.Fatalf("audit events = %+v, want exactly one connection_rejected", evs)
	}

	h.clock.Advance(h.l.rejectLinger)
	hsWaitClosed(t, c)
}

// TestRoleMismatchStillRejectsKnownDialer guards the role check order: a peer
// this Broker dials is still refused with a role mismatch, not with a silent
// close or a "not a peer" audit.
func TestRoleMismatchStillRejectsKnownDialer(t *testing.T) {
	h := newHandshakeHarness(t)
	peer := Fingerprint{0xF4}
	h.peers.Add(peer, pairing.RoleDialer, "peer")

	c, _, p := h.newConn(t, peer)
	res := h.serve(c, nil)

	hsSend(t, p, encodeHello(linkpb.ProtocolVersion, "peer", hsNonce(12), nil))
	rej := hsRecv(t, p)
	reason, err := decodeReject(rej)
	if err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if reason != linkpb.RejectReason_REJECT_REASON_ROLE_MISMATCH {
		t.Fatalf("reject reason = %v, want role mismatch", reason)
	}
	if r := waitHSResult(t, res); r.ok {
		t.Fatal("role mismatch was installed")
	}
	if evs := h.settings.Events(); len(evs) != 0 {
		t.Fatalf("role mismatch wrote audit events: %+v", evs)
	}
}
