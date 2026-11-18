package config

import (
	"testing"
	"time"
)

// TestREQBrokerConfig017_ReloadKeepsInvalid checks that a value which parses
// as the right YAML type but fails the key's own range check is not adopted
// when the file changes. Reload must apply the same validateValue check as Set.
func TestREQBrokerConfig017_ReloadKeepsInvalid(t *testing.T) {
	c, path := newWatchCore(t, "pairing:\n  max_failures: 5\n", false, nil)

	writeConfigBody(t, path, "pairing:\n  max_failures: 0\n")
	c.rescanOnce()

	c.mu.Lock()
	got := c.values["pairing.max_failures"].Int
	c.mu.Unlock()
	if got != 5 {
		t.Fatalf("pairing.max_failures = %d after an invalid reload, want 5", got)
	}
	if n := countEvent(t, c, EventConfigFileChanged, "pairing.max_failures"); n != 0 {
		t.Fatalf("config_file_changed records = %d, want 0", n)
	}
}

// TestREQBrokerConfig017_ReloadAdoptsValid checks the counterpart: a valid new
// value passes the key's own check and is adopted with one audit record.
func TestREQBrokerConfig017_ReloadAdoptsValid(t *testing.T) {
	c, path := newWatchCore(t, "pairing:\n  max_failures: 5\n", false, nil)

	writeConfigBody(t, path, "pairing:\n  max_failures: 7\n")
	c.rescanOnce()

	c.mu.Lock()
	got := c.values["pairing.max_failures"].Int
	c.mu.Unlock()
	if got != 7 {
		t.Fatalf("pairing.max_failures = %d after a valid reload, want 7", got)
	}
	if n := countEvent(t, c, EventConfigFileChanged, "pairing.max_failures"); n != 1 {
		t.Fatalf("config_file_changed records = %d, want 1", n)
	}
}

// TestREQBrokerConfig017_ReloadKeepsInvalidAlias checks the alias path, which
// reload handles separately from the generic per-key loop.
func TestREQBrokerConfig017_ReloadKeepsInvalidAlias(t *testing.T) {
	c, path := newWatchCore(t, "broker:\n  alias: old\n", false, nil)

	writeConfigBody(t, path, "broker:\n  alias: this-alias-is-much-longer-than-twenty-four\n")
	c.rescanOnce()

	if got := aliasOf(t, c); got != "old" {
		t.Fatalf("alias = %q after an invalid reload, want old", got)
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.alias"); n != 0 {
		t.Fatalf("config_file_changed records = %d, want 0", n)
	}
}

// TestConfigReloadListenUpdatesEffectiveAddress checks that adopting a new
// broker.listen also replaces the effective listen address. Reload used to
// write the adopted value into the same map it compared against, so the
// comparison saw no change, the effective address stayed at the old port, and
// the advertised list and default alias kept the old port even though Get
// returned the new value.
func TestConfigReloadListenUpdatesEffectiveAddress(t *testing.T) {
	c, path := newWatchCore(t, "broker:\n  listen: 127.0.0.1:7777\n", false, nil)

	if got := advertisedAddrs(t, c); len(got) != 1 || got[0] != "127.0.0.1:7777" {
		t.Fatalf("initial advertised = %v, want [127.0.0.1:7777]", got)
	}

	writeConfigBody(t, path, "broker:\n  listen: 127.0.0.1:8888\n")
	c.rescanOnce()

	c.mu.Lock()
	stored := c.values["broker.listen"].String
	port := c.effectiveListen.port
	alias := c.values["broker.alias"].String
	c.mu.Unlock()

	if stored != "127.0.0.1:8888" {
		t.Fatalf("broker.listen = %q after reload, want 127.0.0.1:8888", stored)
	}
	if port != 8888 {
		t.Fatalf("effective listen port = %d after reload, want 8888", port)
	}
	if alias != "hostA-8888" {
		t.Fatalf("default alias = %q after reload, want hostA-8888", alias)
	}
	if got := advertisedAddrs(t, c); len(got) != 1 || got[0] != "127.0.0.1:8888" {
		t.Fatalf("advertised = %v after reload, want [127.0.0.1:8888]", got)
	}
}

// recordingTicker is a ticker seam that records how often stop is called and
// the interval it was created with. The channel is never written, so the
// address goroutine blocks until the core is closed.
type recordingTicker struct {
	ch       chan time.Time
	interval time.Duration
	stopped  int
}

func (t *recordingTicker) C() <-chan time.Time { return t.ch }
func (t *recordingTicker) Stop()               { t.stopped++ }

// TestConfigReloadIntervalRestartsTicker checks that adopting a new
// broker.address_check_interval replaces the periodic address ticker. The
// reload compared the new value against the same map it had already written,
// so the comparison was always equal and the ticker kept the old interval.
func TestConfigReloadIntervalRestartsTicker(t *testing.T) {
	c, path := newWatchCore(t, "broker:\n  address_check_interval: 1m\n", false, nil)

	var created []*recordingTicker
	c.mu.Lock()
	c.noBackground = false
	c.newTicker = func(d time.Duration) ticker {
		tick := &recordingTicker{ch: make(chan time.Time), interval: d}
		created = append(created, tick)
		return tick
	}
	c.mu.Unlock()

	writeConfigBody(t, path, "broker:\n  address_check_interval: 2m\n")
	c.rescanOnce()

	c.mu.Lock()
	value := c.values["broker.address_check_interval"].Duration
	running := c.addrTicker
	c.mu.Unlock()

	if value != 2*time.Minute {
		t.Fatalf("broker.address_check_interval = %s after reload, want 2m0s", value)
	}
	if len(created) != 1 {
		t.Fatalf("tickers started after reload = %d, want 1", len(created))
	}
	if created[0].interval != 2*time.Minute {
		t.Fatalf("restarted ticker interval = %s, want 2m0s", created[0].interval)
	}
	if running == nil {
		t.Fatal("addrTicker is nil after reload")
	}
}

// advertisedAddrs returns the advertised addresses in string form.
func advertisedAddrs(t *testing.T, c *core) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.advertised))
	for _, addr := range c.advertised {
		out = append(out, addr.Addr)
	}
	return out
}
