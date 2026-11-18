package link

import (
	"context"
	"fmt"
	"time"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/link/linkpb"
	"github.com/wangzw/agent-peer/internal/pairing"
	"github.com/wangzw/agent-peer/internal/pairing/pairingpb"
)

// nonce returns a fresh random value of nonceSize bytes. The handshake mixes it
// into the pairing salt, so a short read is a failure rather than a shorter
// salt.
func (l *Link) nonce() ([]byte, error) {
	b := make([]byte, nonceSize)
	n, err := l.randRead(b)
	if err != nil {
		return nil, err
	}
	if n != len(b) {
		return nil, fmt.Errorf("link: random source returned %d of %d bytes", n, len(b))
	}
	return b, nil
}

// timerCtx returns a context canceled after d and a release function that stops
// the timer and cancels the context. The caller must call release when done.
func (l *Link) timerCtx(d time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := l.clock.AfterFunc(d, cancel)
	return ctx, func() {
		if stop != nil {
			stop()
		}
		cancel()
	}
}

// rejectAndLinger sends a Reject and keeps the connection open for at most
// rejectLinger after which it is closed. The same deadline bounds the write, so
// a peer that stops reading cannot keep the connection alive. A write failure
// closes at once.
func (l *Link) rejectAndLinger(c *conn, reason linkpb.RejectReason) {
	ctx, cancel := context.WithCancel(context.Background())
	l.clock.AfterFunc(l.rejectLinger, cancel)
	go func() {
		<-ctx.Done()
		c.closeAsync(FailProtocol)
	}()
	if err := c.sendDirect(ctx, encodeReject(reason)); err != nil {
		cancel()
	}
}

// rejectUnknown refuses a connection whose peer is not in the peer table and
// writes the audit event that records the attempt.
func (l *Link) rejectUnknown(c *conn, fp Fingerprint) {
	l.rejectAndLinger(c, linkpb.RejectReason_REJECT_REASON_NOT_A_PEER)
	l.settings.Audit(config.Event{
		Time:   l.clock.Now(),
		Event:  config.EventConnectionRejected,
		Peer:   &config.PeerRef{Fingerprint: fp},
		Detail: "not a peer",
	})
}

// failFromReject maps a peer's reject reason to the local failure reason.
func failFromReject(r linkpb.RejectReason) FailReason {
	switch r {
	case linkpb.RejectReason_REJECT_REASON_NOT_A_PEER:
		return FailNotAPeer
	case linkpb.RejectReason_REJECT_REASON_ROLE_MISMATCH:
		return FailRoleMismatch
	case linkpb.RejectReason_REJECT_REASON_PROTOCOL_VERSION_INCOMPATIBLE:
		return FailVersionIncompatible
	default:
		return FailProtocol
	}
}

// noteHandshaking moves an existing record into the handshaking phase. It does
// nothing when there is no record yet.
func (l *Link) noteHandshaking(fp Fingerprint) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec := l.records[fp]; rec != nil {
		rec.phase = PhaseHandshaking
	}
}

// noteVersionFailure records a version mismatch on an existing record. The
// accepting side stays in PhaseWaiting: it does not redial.
func (l *Link) noteVersionFailure(fp Fingerprint, peerVersion uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec := l.records[fp]; rec != nil {
		rec.lastFailure = FailVersionIncompatible
		rec.peerVersion = peerVersion
	}
}

// serveInbound reads and judges the first frame of an accepted connection,
// answers a Hello or hands a pairing request to pairing, and installs the link
// when the connection is authenticated. It does not run the connection: on
// success the caller must continue with runConn(c, delivered).
//
// It returns (nil, false) on every failure exit; the connection is then closed
// or already closing.
func (l *Link) serveInbound(c *conn, t *trackedConn) (<-chan struct{}, bool) {
	fp := c.id.fp

	f, err := c.recv(context.Background())
	if err != nil {
		return nil, false
	}
	if t != nil {
		t.firstFrameSeen()
	}

	// A pairing request, whatever the fingerprint, belongs to pairing. Pairing
	// pushes the following Hello back, so the normal path below sees it.
	if f.GetKind() == pairingpb.KindPairRequest && classify(f) == classState {
		c.unread(f)
		if err := l.peers.Accept(newPairConn(c)); err != nil {
			c.closeAsync(FailProtocol)
			return nil, false
		}
		if t != nil {
			t.authenticated()
		}
		ctx, release := l.timerCtx(l.helloTimeout)
		next, err := c.recv(ctx)
		release()
		if err != nil {
			c.closeAsync(FailProtocol)
			return nil, false
		}
		f = next
	}

	if f.GetKind() != linkpb.KindHello {
		if l.peers.Role(fp) == pairing.RoleUnknown {
			l.rejectUnknown(c, fp)
			return nil, false
		}
		l.logger.Warn("link: unexpected first frame from a known peer", "peer", fp, "kind", f.GetKind())
		c.closeAsync(FailProtocol)
		return nil, false
	}

	// An unknown fingerprint is refused before its Hello is decoded: a stranger
	// that sends a malformed Hello must still get the "not a peer" Reject and
	// leave the connection_rejected audit record. The check does not depend on
	// the payload, so decoding first only loses the audit.
	if l.peers.Role(fp) == pairing.RoleUnknown {
		l.rejectUnknown(c, fp)
		return nil, false
	}

	hello, err := decodeHello(f)
	if err != nil {
		c.closeAsync(FailProtocol)
		return nil, false
	}

	gen := l.dropGeneration(fp)
	role := l.peers.Role(fp)
	switch role {
	case pairing.RoleUnknown:
		l.rejectUnknown(c, fp)
		return nil, false
	case pairing.RoleDialer:
		l.logger.Warn("link: inbound connection from a peer this Broker dials", "peer", fp)
		l.rejectAndLinger(c, linkpb.RejectReason_REJECT_REASON_ROLE_MISMATCH)
		return nil, false
	}

	if hello.GetProtocolVersion() != l.protocolVersion {
		l.logger.Warn("link: protocol version mismatch", "peer", fp,
			"local", l.protocolVersion, "peer_version", hello.GetProtocolVersion())
		l.noteVersionFailure(fp, hello.GetProtocolVersion())
		l.rejectAndLinger(c, linkpb.RejectReason_REJECT_REASON_PROTOCOL_VERSION_INCOMPATIBLE)
		return nil, false
	}

	l.noteHandshaking(fp)
	l.peers.ObserveAlias(fp, hello.GetAlias())

	// The accepting side reports its advertised addresses; the dialing side's
	// list is ignored.
	nonce, err := l.nonce()
	if err != nil {
		l.logger.Error("link: cannot generate a nonce", "peer", fp, "error", err)
		c.closeAsync(FailProtocol)
		return nil, false
	}
	ctx, release := l.timerCtx(l.helloTimeout)
	sendErr := c.sendDirect(ctx, encodeHello(l.protocolVersion, l.settings.Alias(), nonce, l.settings.Advertised()))
	release()
	if sendErr != nil {
		c.closeAsync(FailProtocol)
		return nil, false
	}

	// The salt is always the dialer's nonce followed by the acceptor's.
	salt := make([]byte, 0, len(hello.GetNonce())+len(nonce))
	salt = append(salt, hello.GetNonce()...)
	salt = append(salt, nonce...)

	if t != nil {
		t.authenticated()
	}
	c.role = pairing.RoleAcceptor
	delivered, ok := l.install(fp, pairing.RoleAcceptor, c, salt, gen)
	if !ok {
		c.closeAsync(FailConnectionLost)
		return nil, false
	}
	return delivered, true
}

// dialerHandshake exchanges Hello frames on a dialed connection whose
// fingerprint was already checked. It returns the salt on success and the
// failure reason otherwise. A failed handshake does not close the connection;
// the caller does.
func (l *Link) dialerHandshake(c *conn) (salt []byte, failure FailReason, peerVersion uint32, err error) {
	fp := c.id.fp

	nonce, err := l.nonce()
	if err != nil {
		return nil, FailProtocol, 0, err
	}

	// The dialer never reports advertised addresses.
	ctx, release := l.timerCtx(l.helloTimeout)
	defer release()
	if sendErr := c.sendDirect(ctx, encodeHello(l.protocolVersion, l.settings.Alias(), nonce, nil)); sendErr != nil {
		return nil, FailConnectionLost, 0, sendErr
	}

	f, err := c.recv(ctx)
	if err != nil {
		return nil, FailConnectionLost, 0, err
	}

	switch f.GetKind() {
	case linkpb.KindReject:
		reason, decodeErr := decodeReject(f)
		if decodeErr != nil {
			return nil, FailProtocol, 0, decodeErr
		}
		return nil, failFromReject(reason), 0, fmt.Errorf("link: peer rejected the link: %v", reason)

	case linkpb.KindHello:
		hello, decodeErr := decodeHello(f)
		if decodeErr != nil {
			return nil, FailProtocol, 0, decodeErr
		}
		if hello.GetProtocolVersion() != l.protocolVersion {
			l.logger.Warn("link: protocol version mismatch", "peer", fp,
				"local", l.protocolVersion, "peer_version", hello.GetProtocolVersion())
			// Both roles send Reject when the versions differ.
			_ = c.sendDirect(ctx, encodeReject(linkpb.RejectReason_REJECT_REASON_PROTOCOL_VERSION_INCOMPATIBLE))
			return nil, FailVersionIncompatible, hello.GetProtocolVersion(),
				fmt.Errorf("link: peer protocol version %d, want %d", hello.GetProtocolVersion(), l.protocolVersion)
		}
		// The address list must reach the peer table before the up
		// notification so a later dial uses the fresh list.
		l.peers.ObserveAlias(fp, hello.GetAlias())
		l.peers.ObserveAcceptorAddresses(fp, addrsFromPB(hello.GetAdvertisedAddresses()))
		salt := make([]byte, 0, len(nonce)+len(hello.GetNonce()))
		salt = append(salt, nonce...)
		salt = append(salt, hello.GetNonce()...)
		return salt, FailNone, 0, nil

	default:
		return nil, FailProtocol, 0, fmt.Errorf("%w: unexpected handshake frame kind %d", errMalformed, f.GetKind())
	}
}
