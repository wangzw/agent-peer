package link

import (
	"context"

	"github.com/wangzw/agent-peer/internal/link/linkpb"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// pairConn adapts a conn to the interface pairing speaks during a handshake.
// Pairing gets the raw frames of one TLS connection; it never parses link
// frames.
type pairConn struct {
	c *conn
}

var _ pairing.PairConn = (*pairConn)(nil)

// newPairConn wraps c for pairing.
func newPairConn(c *conn) pairing.PairConn {
	return &pairConn{c: c}
}

// PeerFingerprint returns the certificate fingerprint the peer presented.
func (p *pairConn) PeerFingerprint() pairing.Fingerprint {
	return p.c.id.fp
}

// PeerPublicKey returns a copy of the peer's raw Ed25519 public key.
func (p *pairConn) PeerPublicKey() []byte {
	return append([]byte(nil), p.c.id.pub...)
}

// RemoteAddr returns the peer's TCP address in host:port form.
func (p *pairConn) RemoteAddr() string {
	return p.c.id.remote
}

// Send writes one unprompted frame. Pairing frames carry no request id and are
// not replies.
func (p *pairConn) Send(ctx context.Context, kind uint32, payload []byte) error {
	return p.c.sendDirect(ctx, &linkpb.Frame{Kind: kind, Payload: payload})
}

// Receive returns the next frame in stream order.
func (p *pairConn) Receive(ctx context.Context) (uint32, []byte, error) {
	f, err := p.c.recv(ctx)
	if err != nil {
		return 0, nil, err
	}
	return f.GetKind(), f.GetPayload(), nil
}

// Unread puts a frame back at the head of the receive queue.
func (p *pairConn) Unread(kind uint32, payload []byte) {
	p.c.unread(&linkpb.Frame{Kind: kind, Payload: payload})
}

// Close closes the connection and reports success; pairing may call it more
// than once.
func (p *pairConn) Close() error {
	p.c.closeAsync(FailProtocol)
	return nil
}
