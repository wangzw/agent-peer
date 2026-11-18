package broker

import (
	"errors"
	"testing"

	"github.com/wangzw/agent-peer/internal/config"
)

// failingConfig returns an error from Get once fail is set, like the
// configuration after ConfigSource.Close during shutdown.
type failingConfig struct {
	ConfigSource
	fail bool
}

func (c *failingConfig) Get(key string) (config.Value, error) {
	if c.fail {
		return config.Value{}, errors.New("config is closed")
	}
	return c.ConfigSource.Get(key)
}

// TestLocalAliasKeepsLastValueAfterGetFailure checks the shutdown defence: once
// Config.Get fails, localAlias must keep returning the last valid alias instead
// of an empty string, which would make FormatAddress panic in an address pushed
// while handlers finish.
func TestLocalAliasKeepsLastValueAfterGetFailure(t *testing.T) {
	cfg := &failingConfig{ConfigSource: newFakeConfig(t.TempDir())}
	b, err := New(Deps{Config: cfg, Topics: newFakeTopics()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.Stop)

	b.mu.Lock()
	got := b.localAlias()
	b.mu.Unlock()
	if got != "hostA" {
		t.Fatalf("localAlias before the failure = %q, want hostA", got)
	}

	cfg.fail = true
	b.mu.Lock()
	got = b.localAlias()
	b.mu.Unlock()
	if got != "hostA" {
		t.Fatalf("localAlias after a failed Get = %q, want the last valid alias hostA", got)
	}
}
