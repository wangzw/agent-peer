package broker

import (
	"context"
	"sync"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// This file holds the doubles for peer management: the pairing surface, the
// link surface, the broker catalog and the Connect warner.

var (
	_ PairingOps    = (*pairing.Pairing)(nil)
	_ OwnAliasSync  = (*pairing.Pairing)(nil)
	_ PeerStore     = (*pairing.Pairing)(nil)
	_ PeerStore     = (*fakePairing)(nil)
	_ PeerLinks     = (*fakePeerLinks)(nil)
	_ BrokerCatalog = (*fakeCatalog)(nil)
	_ ConnectWarner = (*fakeWarner)(nil)
)

// fakePairing is an in-memory PeerStore. Tests set connectFn and keyFn to drive
// the two calls that reach the network; everything else works on the peer list.
type fakePairing struct {
	mu    sync.Mutex
	peers []pairing.Peer

	connectFn func(ctx context.Context, address, key, localAlias string) (pairing.Peer, string, error)
	keyFn     func() (string, time.Time, []string, error)
	unpairErr error

	ownAlias string
	taken    map[string]bool // AliasConflict's answers
	calls    []string
	keys     []string // keys Connect received; kept out of calls so logs stay key-free
}

func (f *fakePairing) recordLocked(call string) { f.calls = append(f.calls, call) }

func (f *fakePairing) Peers() []pairing.Peer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Peers")
	return append([]pairing.Peer(nil), f.peers...)
}

func (f *fakePairing) EffectiveAlias(fp pairing.Fingerprint) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("EffectiveAlias")
	for _, p := range f.peers {
		if p.Fingerprint == fp {
			if p.LocalAlias != "" {
				return p.LocalAlias
			}
			return p.Alias
		}
	}
	return ""
}

func (f *fakePairing) ResolveAlias(alias string) (pairing.Fingerprint, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("ResolveAlias")
	for _, p := range f.peers {
		effective := p.Alias
		if p.LocalAlias != "" {
			effective = p.LocalAlias
		}
		if effective == alias {
			return p.Fingerprint, true
		}
	}
	return pairing.Fingerprint{}, false
}

func (f *fakePairing) IsPeer(fp pairing.Fingerprint) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("IsPeer")
	for _, p := range f.peers {
		if p.Fingerprint == fp {
			return true
		}
	}
	return false
}

func (f *fakePairing) Role(fp pairing.Fingerprint) pairing.Role {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Role")
	for _, p := range f.peers {
		if p.Fingerprint == fp {
			return p.Role
		}
	}
	return pairing.RoleUnknown
}

func (f *fakePairing) Connect(ctx context.Context, address, key, localAlias string) (pairing.Peer, string, error) {
	f.mu.Lock()
	f.recordLocked("Connect " + address + " " + localAlias)
	f.keys = append(f.keys, key)
	fn := f.connectFn
	f.mu.Unlock()
	if fn == nil {
		return pairing.Peer{}, "", pairing.ErrNotOpen
	}
	return fn(ctx, address, key, localAlias)
}

func (f *fakePairing) CreateOneTimeKey() (key string, expires time.Time, shown []string, err error) {
	f.mu.Lock()
	f.recordLocked("CreateOneTimeKey")
	fn := f.keyFn
	f.mu.Unlock()
	if fn == nil {
		return "", time.Time{}, nil, pairing.ErrNotListening
	}
	return fn()
}

func (f *fakePairing) Unpair(fp pairing.Fingerprint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Unpair")
	if f.unpairErr != nil {
		return f.unpairErr
	}
	kept := f.peers[:0]
	for _, p := range f.peers {
		if p.Fingerprint != fp {
			kept = append(kept, p)
		}
	}
	f.peers = kept
	return nil
}

func (f *fakePairing) SetOwnAlias(alias string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("SetOwnAlias")
	f.ownAlias = alias
}

func (f *fakePairing) AliasConflict(alias string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("AliasConflict")
	return f.taken[alias]
}

// fakePeerLinks is an in-memory PeerLinks. PairDial calls pairDialFn when set;
// Status answers from the map. calls records Kick and Drop only.
type fakePeerLinks struct {
	mu         sync.Mutex
	status     map[pairing.Fingerprint]PeerLinkStatus
	pairDialFn func(ctx context.Context, address string, run func(pairing.PairConn) error) error
	calls      []string
}

func (f *fakePeerLinks) PairDial(ctx context.Context, address string, run func(pairing.PairConn) error) error {
	f.mu.Lock()
	fn := f.pairDialFn
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, address, run)
}

func (f *fakePeerLinks) Drop(fp pairing.Fingerprint) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Drop "+fp.String()[:8])
}

func (f *fakePeerLinks) Kick(fp pairing.Fingerprint) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Kick "+fp.String()[:8])
}

func (f *fakePeerLinks) Status(fp pairing.Fingerprint) PeerLinkStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[fp]
}

// fakeCatalog is an in-memory BrokerCatalog.
type fakeCatalog struct {
	cards    map[pairing.Fingerprint][]config.AdvertisedAddr
	nonPeers []*localpb.BrokerInfo
}

func (f *fakeCatalog) CardAddresses(fp pairing.Fingerprint) []config.AdvertisedAddr {
	return append([]config.AdvertisedAddr(nil), f.cards[fp]...)
}

func (f *fakeCatalog) NonPeers() []*localpb.BrokerInfo {
	return append([]*localpb.BrokerInfo(nil), f.nonPeers...)
}

// fakeWarner returns fixed warnings and remembers every peer it saw.
type fakeWarner struct {
	warnings []*localpb.Warning
	got      []pairing.Peer
}

func (f *fakeWarner) ConnectWarnings(peer pairing.Peer) []*localpb.Warning {
	f.got = append(f.got, peer)
	return f.warnings
}

// auditsOf returns the audit events of one kind, in write order.
func auditsOf(cfg *fakeConfig, kind config.EventKind) []config.Event {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	var out []config.Event
	for _, e := range cfg.audits {
		if e.Event == kind {
			out = append(out, e)
		}
	}
	return out
}

// withPeers installs the pairing and link doubles. Deps.Peers is filled by the
// peer-info work item, which owns newPeerView.
func withPeers(p *fakePairing, l *fakePeerLinks) func(*Deps) {
	return func(d *Deps) {
		d.Pairing = p
		d.PeerLinks = l
	}
}

// fp returns the fingerprint whose 32 bytes are all b.
func fp(b byte) pairing.Fingerprint {
	var f pairing.Fingerprint
	for i := range f {
		f[i] = b
	}
	return f
}
