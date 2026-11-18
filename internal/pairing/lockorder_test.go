package pairing

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// lockedAliasConfig models the mutex config holds around one key. set takes
// that lock and runs the registered validator while holding it, exactly as
// config.Set and config's file reload do for broker.alias. It then commits the
// value and calls the subscriber after releasing the lock, mirroring the
// notification goroutine.
type lockedAliasConfig struct {
	mu        sync.Mutex
	alias     string
	validator func(newAlias string) error
	onCommit  func(newAlias string)
}

func (c *lockedAliasConfig) set(alias string) error {
	c.mu.Lock()
	if c.validator != nil {
		if err := c.validator(alias); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	c.alias = alias
	c.mu.Unlock()
	if c.onCommit != nil {
		c.onCommit(alias)
	}
	return nil
}

// get reads the value through the same lock, standing in for config.Get.
func (c *lockedAliasConfig) get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alias, nil
}

// TestAliasValidationKeepsTheConfigPairingLockOrder runs the broker.alias
// validator, which holds config's lock, next to handshake commits, which hold
// the peer table lock. The validator calls AliasConflict and ResolveAlias.
//
// The rule is config lock -> pairing lock, never the reverse: pairing must not
// call config while its table lock is held. On the pre-fix code the validator's
// ResolveAlias re-entered config.Get while config's lock was held, and a
// concurrent commit read the own alias through config.Get under the table lock,
// so this test deadlocked. The three-second bound turns that into a failure.
func TestAliasValidationKeepsTheConfigPairingLockOrder(t *testing.T) {
	const bound = 3 * time.Second
	const rounds = 100

	cfg := &lockedAliasConfig{alias: "self"}
	s := newHSSettings(t)
	s.alias = "self"
	s.aliasFunc = cfg.get // reading the own alias takes config's lock
	p := hsNewPairing(t, s, &hsTransport{}, newHSClock())
	p.SetOwnAlias("self")

	cfg.validator = func(newAlias string) error {
		if p.AliasConflict(newAlias) {
			return fmt.Errorf("alias %q conflicts with a peer", newAlias)
		}
		if _, ok := p.ResolveAlias(newAlias); ok {
			return fmt.Errorf("alias %q resolves to a peer", newAlias)
		}
		return nil
	}

	type peerKey struct {
		fp    Fingerprint
		pub   []byte
		alias string
	}
	keys := make([]peerKey, rounds)
	for i := range keys {
		fp, pub := peerTestKey(t)
		keys[i] = peerKey{fp: fp, pub: pub, alias: fmt.Sprintf("peer-%d", i)}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)

	// Config side: hold config's lock, run the validator, commit the value.
	go func() {
		<-start
		for i := 0; i < rounds; i++ {
			if err := cfg.set(fmt.Sprintf("own-%d", i)); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()

	// Pairing side: complete handshakes, each reserving and committing an
	// alias under the peer table lock.
	go func() {
		<-start
		for i := 0; i < rounds; i++ {
			token, ok := p.peers.reserveAlias(keys[i].alias, keys[i].fp)
			if !ok {
				errs <- fmt.Errorf("reserve %q failed", keys[i].alias)
				return
			}
			entry := peerTestEntry(keys[i].fp, keys[i].pub, keys[i].alias, RoleAcceptor)
			if _, err := commitHandshakeForTest(p, entry, token); err != nil {
				errs <- fmt.Errorf("commit %q: %w", keys[i].alias, err)
				return
			}
		}
		errs <- nil
	}()

	close(start)
	deadline := time.After(bound)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("validator or commit failed: %v", err)
			}
		case <-deadline:
			t.Fatalf("validator and handshake commit did not finish within %s: config and pairing locks are inverted", bound)
		}
	}
}
