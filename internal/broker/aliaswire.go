package broker

import (
	"fmt"

	"github.com/wangzw/agent-peer/internal/config"
)

// aliasConflictCheck returns the validation function for broker.alias. The
// function runs under the config lock, so it may call only AliasConflict,
// which takes just the pairing lock; any other pairing query could deadlock
// against a concurrent config access. An empty alias is never a conflict.
func aliasConflictCheck(p OwnAliasSync) func(old, new config.Value) error {
	return func(_, new config.Value) error {
		if new.String == "" {
			return nil
		}
		if p.AliasConflict(new.String) {
			return fmt.Errorf("alias %q is already the effective alias of a peer Broker; choose another alias or unpair that peer first", new.String)
		}
		return nil
	}
}

// wireOwnAlias keeps pairing's own-alias cache in step with broker.alias. It
// subscribes first, registers the conflict check, and then pushes the current
// effective value once, so a change between subscription and first read is not
// lost and the cache is populated before any link can handshake. A rename that
// lands between a read and its push would otherwise leave the stale value in
// the cache, so each push is followed by a fresh read and repeated until two
// reads agree.
func wireOwnAlias(cfg ConfigSource, p OwnAliasSync) error {
	cfg.Subscribe("broker.alias", func(_, new config.Value) {
		p.SetOwnAlias(new.String)
	})
	cfg.Validate("broker.alias", aliasConflictCheck(p))
	cur, err := cfg.Get("broker.alias")
	if err != nil {
		return fmt.Errorf("reading broker.alias: %w", err)
	}
	for {
		p.SetOwnAlias(cur.String)
		next, err := cfg.Get("broker.alias")
		if err != nil {
			return fmt.Errorf("reading broker.alias: %w", err)
		}
		if next.String == cur.String {
			return nil
		}
		cur = next
	}
}
