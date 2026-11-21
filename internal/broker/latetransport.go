package broker

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/wangzw/peers/internal/pairing"
)

// transportTarget is the part of link that pairing's Transport forwards to.
// PeerLinks satisfies it.
type transportTarget interface {
	PairDial(ctx context.Context, address string, run func(pairing.PairConn) error) error
	Drop(fp pairing.Fingerprint)
}

// lateTransport lets pairing be opened before link exists. Assembly opens
// pairing with it, constructs link, and then binds link with set. Until then a
// dial fails as a dial failure and closing a link is a no-op.
type lateTransport struct {
	target atomic.Pointer[transportTarget]
}

// set binds the target. It may be called only once; a second call panics,
// because a second bind would leave pairing dialing through a transport that
// the caller no longer controls.
func (t *lateTransport) set(target transportTarget) {
	if !t.target.CompareAndSwap(nil, &target) {
		panic("broker: transport target set twice")
	}
}

// PairDial reports ErrDialFailed while no target is bound, so pairing maps the
// attempt to an unreachable peer rather than treating it as a handshake error.
func (t *lateTransport) PairDial(ctx context.Context, address string, run func(pairing.PairConn) error) error {
	target := t.target.Load()
	if target == nil {
		return fmt.Errorf("%w: link is not started", pairing.ErrDialFailed)
	}
	return (*target).PairDial(ctx, address, run)
}

// Drop does nothing while no target is bound.
func (t *lateTransport) Drop(fp pairing.Fingerprint) {
	if target := t.target.Load(); target != nil {
		(*target).Drop(fp)
	}
}

var _ pairing.Transport = (*lateTransport)(nil)
