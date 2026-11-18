package broker

import (
	"context"

	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// Assembly carries the concrete upstream instances built by earlier
// assemblers. Later assemblers (topic, directory, delivery) read Link and
// Pairing instead of constructing their own.
type Assembly struct {
	Pairing             *pairing.Pairing
	Link                *link.Link
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

// DefaultAssemblers is used when RunOptions.Assemblers is nil. It builds pairing
// and link first, then the topic registry, then the delivery; the directory
// assembler, when it exists, sits between the topic and the delivery.
// configWiring is last: it sees the link and the card issuer the earlier
// assemblers built, and its subscriptions are registered after pairing's own
// broker.alias subscription.
func DefaultAssemblers() []Assembler {
	return []Assembler{newPeerAssembler(), topicAssembler{}, newDeliveryAssembler(), &configWiring{}}
}
