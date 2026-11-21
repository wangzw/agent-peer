package broker

import (
	"fmt"

	"github.com/wangzw/peers/internal/config"
)

// listenProbeCheck returns the validator for broker.listen. A value equal to
// the current one is accepted without probing: the running link may hold the
// address, so setting it to the value it already has must still succeed. Any
// other value is probed, which binds and immediately releases the address; the
// returned function keeps no listener and calls no config function, so it is
// safe to run under the config lock. In production probe is config.ProbeListen.
func listenProbeCheck(probe func(string) error) func(old, new config.Value) error {
	return func(old, new config.Value) error {
		if old.String == new.String {
			return nil
		}
		if err := probe(new.String); err != nil {
			return fmt.Errorf("address %q is unavailable: %v", new.String, err)
		}
		return nil
	}
}
