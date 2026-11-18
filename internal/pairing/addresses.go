package pairing

import (
	"log/slog"

	"github.com/wangzw/agent-peer/internal/config"
)

// maxCandidatesPerSource is the cap on one acceptor or card address list. The
// cap keeps peers.json bounded no matter how large a list a peer advertises.
const maxCandidatesPerSource = 8

// filterKnown keeps only the address entries this version understands and
// returns them as a new slice. Unknown kinds are skipped without disturbing the
// recognised entries around them. Empty addresses are dropped as well: they can
// only come from a malformed or hostile peer and cannot be dialed.
func filterKnown(addrs []config.AdvertisedAddr) []config.AdvertisedAddr {
	out := make([]config.AdvertisedAddr, 0, len(addrs))
	for _, a := range addrs {
		if a.Kind != config.AdvertisedKindDirect {
			continue
		}
		if a.Addr == "" {
			continue
		}
		out = append(out, config.AdvertisedAddr{Kind: a.Kind, Addr: a.Addr})
	}
	return out
}

// capKnown filters a list and keeps at most maxCandidatesPerSource entries,
// dropping from the end.
func capKnown(addrs []config.AdvertisedAddr) []config.AdvertisedAddr {
	out := filterKnown(addrs)
	if len(out) > maxCandidatesPerSource {
		out = out[:maxCandidatesPerSource]
	}
	return out
}

// mergeCandidateList merges the stored sources in the order acceptor, card,
// pairing, removes duplicate addresses keeping the first occurrence, and
// records the source each surviving address came from. Peers for which this
// Broker is the acceptor have no candidates.
func mergeCandidateList(e *peerEntry) []Candidate {
	if e == nil || e.role != RoleDialer {
		return nil
	}

	var out []Candidate
	seen := make(map[string]struct{})

	appendAddr := func(addr string, source AddressSource) {
		if addr == "" {
			return
		}
		if _, dup := seen[addr]; dup {
			return
		}
		seen[addr] = struct{}{}
		out = append(out, Candidate{Source: source, Addr: addr})
	}

	for _, a := range e.acceptor {
		if a.Kind != config.AdvertisedKindDirect {
			continue
		}
		appendAddr(a.Addr, SourceAcceptor)
	}
	for _, a := range e.card {
		if a.Kind != config.AdvertisedKindDirect {
			continue
		}
		appendAddr(a.Addr, SourceCard)
	}
	appendAddr(e.pairingAddr, SourcePairing)

	return out
}

// mergeCandidates returns the merged candidate addresses and the last connected
// address for a dialer peer. An acceptor peer yields no addresses and no last
// connected address. The last connected address is returned even when no
// candidate source is populated: link can still redial the address that last
// worked.
func mergeCandidates(e *peerEntry) ([]string, string) {
	if e == nil || e.role != RoleDialer {
		return nil, ""
	}
	list := mergeCandidateList(e)
	if len(list) == 0 {
		return nil, e.lastConnected
	}
	addrs := make([]string, 0, len(list))
	for _, c := range list {
		addrs = append(addrs, c.Addr)
	}
	return addrs, e.lastConnected
}

// ObserveAcceptorAddresses replaces the acceptor-sourced candidate list of a
// dialer peer. Unknown address kinds are dropped and the list is capped. Calls
// for unknown or acceptor peers are ignored; persistence failures keep the old
// list and are logged.
func (p *Pairing) ObserveAcceptorAddresses(fp Fingerprint, addresses []config.AdvertisedAddr) {
	if p == nil || p.peers == nil {
		slog.Error("pairing: ObserveAcceptorAddresses on an unopened instance", "fingerprint", fingerprintHex(fp))
		return
	}
	if err := p.peers.updateSource(fp, SourceAcceptor, addresses); err != nil {
		slog.Error("pairing: persist acceptor addresses", "fingerprint", fingerprintHex(fp), "error", err)
	}
}

// UpdateCardAddresses replaces the directory-card-sourced candidate list of a
// dialer peer. Unknown address kinds are dropped and the list is capped. Calls
// for unknown or acceptor peers are ignored; persistence failures keep the old
// list and are logged.
func (p *Pairing) UpdateCardAddresses(fp Fingerprint, addresses []config.AdvertisedAddr) {
	if p == nil || p.peers == nil {
		slog.Error("pairing: UpdateCardAddresses on an unopened instance", "fingerprint", fingerprintHex(fp))
		return
	}
	if err := p.peers.updateSource(fp, SourceCard, addresses); err != nil {
		slog.Error("pairing: persist card addresses", "fingerprint", fingerprintHex(fp), "error", err)
	}
}

// Candidates returns the candidate addresses of a dialer peer in dial order and
// the last connected address. Acceptor peers and unknown fingerprints have no
// candidates.
func (p *Pairing) Candidates(fp Fingerprint) (addresses []string, last string) {
	if p == nil || p.peers == nil {
		return nil, ""
	}
	entry, ok := p.peers.get(fp)
	if !ok {
		return nil, ""
	}
	return mergeCandidates(&entry)
}
