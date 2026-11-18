package pairing

import (
	"crypto/rand"
	"testing"
)

// TestOpenSeedsOwnAliasCache checks that opening a Pairing fills the peer
// table's own-alias cache, so every path that observes an alias sees the local
// alias even before the Broker calls SetOwnAlias.
//
// A peer that reports the Broker's own alias must get a generated local alias.
// When the cache starts empty, observeAlias treats the reported alias as free
// and makes it the peer's effective alias, which collides with the Broker
// itself.
func TestOpenSeedsOwnAliasCache(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		open func(t *testing.T, s *hsSettings) *Pairing
	}{
		{
			name: "newPairing",
			open: func(t *testing.T, s *hsSettings) *Pairing {
				t.Helper()
				p, err := newPairing(s, &hsTransport{}, newHSClock(), realFileOps{}, rand.Read)
				if err != nil {
					t.Fatalf("newPairing: %v", err)
				}
				return p
			},
		},
		{
			name: "hsNewPairing",
			open: func(t *testing.T, s *hsSettings) *Pairing {
				t.Helper()
				return hsNewPairing(t, s, &hsTransport{}, newHSClock())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newHSSettings(t)
			s.alias = "beta"
			p := tc.open(t, s)

			fp, pub := hsKey(t)
			if _, err := p.peers.put(peerEntry{
				fingerprint: fp,
				publicKey:   pub,
				alias:       "gamma",
				role:        RoleAcceptor,
				pairedAt:    p.clock.Now(),
			}); err != nil {
				t.Fatalf("seed peer table: %v", err)
			}

			// Deliberately no SetOwnAlias: the open path must have seeded the
			// cache with the configured alias.
			p.ObserveAlias(fp, "beta")

			if got := p.EffectiveAlias(fp); got == "beta" {
				t.Fatalf("peer kept effective alias %q after reporting the local alias; want a generated local alias", got)
			}
			entry, ok := p.peers.get(fp)
			if !ok {
				t.Fatal("peer disappeared from the table")
			}
			if entry.localAlias == "" {
				t.Fatalf("no local alias generated: %+v", entry)
			}
		})
	}
}
