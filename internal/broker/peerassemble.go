package broker

import (
	"context"
	"fmt"

	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// peerAssembler builds pairing and link and connects them to each other and to
// the Broker's peer surfaces. It only constructs instances: Run binds the
// listener and starts link after the local socket is ready.
//
// The three constructor seams exist so unit tests can substitute doubles; the
// production values are newPeerAssembler's defaults.
type peerAssembler struct {
	openPairing func(t pairing.Transport) (PeerStore, *pairing.Pairing, error)
	newLink     func(opts link.Options) (*link.Link, error)
	adapt       func(l *link.Link) (PeerLinks, LinkRunner)
}

var _ Assembler = (*peerAssembler)(nil)

// newPeerAssembler returns the production assembler.
func newPeerAssembler() *peerAssembler {
	return &peerAssembler{
		openPairing: func(t pairing.Transport) (PeerStore, *pairing.Pairing, error) {
			p, err := pairing.Open(t)
			if err != nil {
				return nil, nil, err
			}
			return p, p, nil
		},
		newLink: link.New,
		adapt: func(l *link.Link) (PeerLinks, LinkRunner) {
			return peerLinkAdapter{l}, l
		},
	}
}

// Name is the assembler's name in startup errors.
func (*peerAssembler) Name() string { return "pairing and link" }

// Assemble opens pairing, wires broker.alias into it, builds link over the same
// pairing instance, binds the late transport, and fills both Deps and the
// Assembly. The alias wiring happens before link exists so no link can
// handshake while pairing's alias cache is still empty.
func (a *peerAssembler) Assemble(ctx context.Context, cfg ConfigSource, deps *Deps, asm *Assembly) ([]Component, error) {
	lt := &lateTransport{}
	store, concrete, err := a.openPairing(lt)
	if err != nil {
		return nil, fmt.Errorf("loading identity and peer table: %w", err)
	}

	if err := wireOwnAlias(cfg, store); err != nil {
		return nil, err
	}

	lk, err := a.newLink(link.Options{
		Peers:           concrete,
		Settings:        link.ConfigSettings(),
		Clock:           link.RealClock(),
		Logger:          deps.Logger,
		ProtocolVersion: asm.LinkProtocolVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("creating link: %w", err)
	}
	links, runner := a.adapt(lk)

	lt.set(links)

	deps.Pairing = store
	deps.PeerLinks = links
	deps.Peers = newPeerViewFunc(store, links, func() BrokerCatalog { return deps.Catalog })

	asm.Pairing = concrete
	asm.Link = lk
	asm.Runner = runner

	return nil, nil
}
