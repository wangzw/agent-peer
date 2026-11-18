package topic

import (
	"bytes"
	"sort"
)

// addLocal adds a local topic and its digest under this link's salt.
func (ps *peerState) addLocal(name string) {
	d := digest(ps.salt, name)
	ps.digest[name] = d
	ps.name[d] = name
}

// removeLocal removes a local topic and its digest. The intersection is not
// recomputed here; the caller does that.
func (ps *peerState) removeLocal(name string) {
	d, ok := ps.digest[name]
	if !ok {
		return
	}
	delete(ps.digest, name)
	delete(ps.name, d)
}

// localDigests returns the digests of every local topic, ascending.
func (ps *peerState) localDigests() [][32]byte {
	out := make([][32]byte, 0, len(ps.digest))
	for _, d := range ps.digest {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i][:], out[j][:]) < 0
	})
	return out
}

// setRemote replaces the peer's digest set with a copy of set.
func (ps *peerState) setRemote(set map[[32]byte]struct{}) {
	remote := make(map[[32]byte]struct{}, len(set))
	for d := range set {
		remote[d] = struct{}{}
	}
	ps.remote = remote
}

// knowsRemote reports whether d is in the peer's latest digest set.
func (ps *peerState) knowsRemote(d [32]byte) bool {
	_, ok := ps.remote[d]
	return ok
}

// recompute replaces the intersection with the digests that are both local and
// in the peer's set, and returns the digests that entered it, ascending.
// A digest that leaves the intersection loses its roster: entries that went
// stale while the topic was outside the intersection must not come back when it
// re-enters, so the peer has to send a new Roster.
func (ps *peerState) recompute() (entered [][32]byte) {
	shared := make(map[[32]byte]struct{}, len(ps.name))
	for d := range ps.name {
		if _, ok := ps.remote[d]; ok {
			shared[d] = struct{}{}
		}
	}
	for d := range ps.shared {
		if _, ok := shared[d]; !ok {
			delete(ps.rosters, d)
		}
	}
	for d := range shared {
		if _, ok := ps.shared[d]; !ok {
			entered = append(entered, d)
		}
	}
	ps.shared = shared
	sort.Slice(entered, func(i, j int) bool {
		return bytes.Compare(entered[i][:], entered[j][:]) < 0
	})
	return entered
}

// sharedDigest returns the digest of a local topic when it is in the
// intersection.
func (ps *peerState) sharedDigest(name string) ([32]byte, bool) {
	d, ok := ps.digest[name]
	if !ok {
		return [32]byte{}, false
	}
	if _, ok := ps.shared[d]; !ok {
		return [32]byte{}, false
	}
	return d, true
}

// applyRoster replaces the peer's entries under d. Entries with an empty Ref are
// skipped, and a later entry with the same Ref overwrites an earlier one.
func (ps *peerState) applyRoster(d [32]byte, ms []Member) {
	entries := make(map[string]Member, len(ms))
	for _, m := range ms {
		if m.Ref == "" {
			continue
		}
		entries[m.Ref] = m
	}
	ps.rosters[d] = entries
}

// applyDelta applies removed, then added, then changed under d. A removal only
// takes effect when both the Ref and the name match; added and changed entries
// are written by Ref, overwriting or creating the entry, and an empty Ref is
// skipped. A digest without a roster starts from an empty table.
func (ps *peerState) applyDelta(d [32]byte, added []Member, removed []memberKey, changed []Member) {
	entries := ps.rosters[d]
	if entries == nil {
		entries = map[string]Member{}
	}
	for _, k := range removed {
		if cur, ok := entries[k.Ref]; ok && cur.Name == k.Name {
			delete(entries, k.Ref)
		}
	}
	for _, m := range added {
		if m.Ref == "" {
			continue
		}
		entries[m.Ref] = m
	}
	for _, m := range changed {
		if m.Ref == "" {
			continue
		}
		entries[m.Ref] = m
	}
	ps.rosters[d] = entries
}
