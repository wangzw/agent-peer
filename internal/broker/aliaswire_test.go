package broker

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/wangzw/peers/internal/config"
)

// ownAliasOf reads the double's cache under its lock; the tests run
// sequentially but the -race build must see no unsynchronized access.
func ownAliasOf(p *fakePairing) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ownAlias
}

// callsOf copies the double's call log under its lock.
func callsOf(p *fakePairing) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func clearCalls(p *fakePairing) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

func stringValue(s string) config.Value {
	return config.Value{Kind: config.KindString, String: s}
}

// TestWireOwnAliasPushesInitialValue covers REQ-broker-pairing-003: the alias
// cache is filled at startup, not only on the first change.
func TestWireOwnAliasPushesInitialValue(t *testing.T) {
	cfg := newFakeConfig(t.TempDir())
	p := &fakePairing{}

	if err := wireOwnAlias(cfg, p); err != nil {
		t.Fatalf("wireOwnAlias: %v", err)
	}
	if got := ownAliasOf(p); got != "hostA" {
		t.Errorf("own alias = %q after wireOwnAlias, want %q", got, "hostA")
	}
}

// TestWireOwnAliasFollowsChanges covers REQ-broker-pairing-003: a change to
// broker.alias reaches the pairing cache.
func TestWireOwnAliasFollowsChanges(t *testing.T) {
	cfg := newFakeConfig(t.TempDir())
	p := &fakePairing{}
	if err := wireOwnAlias(cfg, p); err != nil {
		t.Fatalf("wireOwnAlias: %v", err)
	}

	cfg.setValue("broker.alias", stringValue("hostX"))
	if got := ownAliasOf(p); got != "hostX" {
		t.Errorf("own alias = %q after change, want %q", got, "hostX")
	}
}

// racingGetConfig fires a rename after Get has read the value but before it
// returns it, so the initial push reaches pairing after the subscriber already
// saw the new value.
type racingGetConfig struct {
	*fakeConfig
	once sync.Once
}

func (c *racingGetConfig) Get(key string) (config.Value, error) {
	v, err := c.fakeConfig.Get(key)
	if err != nil {
		return v, err
	}
	c.once.Do(func() {
		c.setValue(key, stringValue("hostY"))
	})
	return v, nil
}

// TestWireOwnAliasRepushesIfChangedConcurrently covers REQ-broker-pairing-003:
// a rename that lands between the initial read and the push must not leave the
// stale value in the cache.
func TestWireOwnAliasRepushesIfChangedConcurrently(t *testing.T) {
	cfg := &racingGetConfig{fakeConfig: newFakeConfig(t.TempDir())}
	p := &fakePairing{}

	if err := wireOwnAlias(cfg, p); err != nil {
		t.Fatalf("wireOwnAlias: %v", err)
	}
	if got := ownAliasOf(p); got != "hostY" {
		t.Fatalf("own alias = %q after a concurrent rename, want %q", got, "hostY")
	}
}

// TestAliasValidatorOnlyCallsAliasConflict keeps the validator inside the lock
// order the design allows: under the config lock it may only ask
// AliasConflict, which takes only the pairing lock. The error must name the
// alias but no peer fingerprint.
func TestAliasValidatorOnlyCallsAliasConflict(t *testing.T) {
	cfg := newFakeConfig(t.TempDir())
	p := &fakePairing{taken: map[string]bool{"hostB": true}}
	if err := wireOwnAlias(cfg, p); err != nil {
		t.Fatalf("wireOwnAlias: %v", err)
	}
	actor := config.Actor{Account: "alice"}

	clearCalls(p)
	if _, err := cfg.Set(actor, "broker.alias", stringValue("hostB")); err == nil {
		t.Fatal("Set(broker.alias=hostB) succeeded, want a conflict error")
	} else {
		if !strings.Contains(err.Error(), "hostB") {
			t.Errorf("conflict error %q does not name the alias", err)
		}
		if regexp.MustCompile(`[0-9a-f]{64}`).MatchString(err.Error()) {
			t.Errorf("conflict error %q leaks a peer fingerprint", err)
		}
	}
	for _, call := range callsOf(p) {
		if call != "AliasConflict" {
			t.Errorf("rejected Set called %q, want only AliasConflict", call)
		}
	}
	if cur, err := cfg.Get("broker.alias"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if cur.String != "hostA" {
		t.Errorf("broker.alias = %q after a rejected Set, want it unchanged (%q)", cur.String, "hostA")
	}

	for _, alias := range []string{"hostZ", ""} {
		clearCalls(p)
		if _, err := cfg.Set(actor, "broker.alias", stringValue(alias)); err != nil {
			t.Fatalf("Set(broker.alias=%q): %v", alias, err)
		}
		for _, call := range callsOf(p) {
			// SetOwnAlias comes from the change subscriber, not the validator.
			if call != "AliasConflict" && call != "SetOwnAlias" {
				t.Errorf("Set(%q) called %q, want only AliasConflict and SetOwnAlias", alias, call)
			}
		}
	}

	// An empty alias must not be conflict-checked at all.
	clearCalls(p)
	if _, err := cfg.Set(actor, "broker.alias", stringValue("")); err != nil {
		t.Fatalf(`Set(broker.alias=""): %v`, err)
	}
	for _, call := range callsOf(p) {
		if call != "SetOwnAlias" {
			t.Errorf(`empty alias invoked %q, want no conflict check`, call)
		}
	}
}

// TestAliasValidatorRegisteredOnce makes sure the conflict check is registered
// exactly once, on broker.alias, and nowhere else.
func TestAliasValidatorRegisteredOnce(t *testing.T) {
	cfg := newFakeConfig(t.TempDir())
	p := &fakePairing{}
	if err := wireOwnAlias(cfg, p); err != nil {
		t.Fatalf("wireOwnAlias: %v", err)
	}

	cfg.mu.Lock()
	aliasChecks := len(cfg.validators["broker.alias"])
	listenChecks := len(cfg.validators["broker.listen"])
	cfg.mu.Unlock()

	if aliasChecks != 1 {
		t.Errorf("broker.alias has %d validators, want 1", aliasChecks)
	}
	if listenChecks != 0 {
		t.Errorf("broker.listen has %d validators, want 0", listenChecks)
	}
}

// errGetConfig makes Get fail while every other ConfigSource method still
// delegates to the fake.
type errGetConfig struct {
	*fakeConfig
	err error
}

func (c errGetConfig) Get(string) (config.Value, error) {
	return config.Value{}, c.err
}

// TestWireOwnAliasGetError checks that a failed initial read surfaces and
// leaves the cache untouched.
func TestWireOwnAliasGetError(t *testing.T) {
	base := newFakeConfig(t.TempDir())
	boom := errors.New("boom")
	cfg := errGetConfig{fakeConfig: base, err: boom}
	p := &fakePairing{}

	err := wireOwnAlias(cfg, p)
	if err == nil {
		t.Fatal("wireOwnAlias succeeded, want a read error")
	}
	if !strings.Contains(err.Error(), "reading broker.alias") {
		t.Errorf("error %q does not say which key failed", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %q does not wrap the Get error", err)
	}
	if got := ownAliasOf(p); got != "" {
		t.Errorf("own alias = %q after a failed initial read, want empty", got)
	}
	for _, call := range callsOf(p) {
		if call == "SetOwnAlias" {
			t.Errorf("SetOwnAlias called after a failed initial read: %v", callsOf(p))
			break
		}
	}
}
