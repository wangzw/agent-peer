package broker

import (
	"context"

	"github.com/wangzw/agent-peer/internal/pairing"
)

// Assembly carries the concrete upstream instances built by earlier
// assemblers. The Link *link.Link field is added by the broker-peers unit once
// link is on main.
type Assembly struct {
	Pairing             *pairing.Pairing
	Runner              LinkRunner
	LinkProtocolVersion uint32 // 0: linkpb.ProtocolVersion
}

// Assembler builds one piece of upstream state before the Broker is
// constructed. Assemblers run in order; each receives the same Assembly so a
// later one can read or replace what an earlier one built, and the same Deps so
// it can substitute an implementation before New copies them.
type Assembler interface {
	Name() string
	Assemble(ctx context.Context, cfg ConfigSource, deps *Deps, asm *Assembly) ([]Component, error)
}

// DefaultAssemblers is used when RunOptions.Assemblers is nil. Later units add,
// in order: pairing and link, topic, directory, delivery, config subscriptions.
func DefaultAssemblers() []Assembler { return nil }
