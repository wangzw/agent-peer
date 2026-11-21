package broker

import (
	"context"

	"github.com/wangzw/peers/internal/link"
	"github.com/wangzw/peers/internal/pairing"
)

// Assembly carries the concrete upstream instances built by earlier
// assemblers. Later assemblers (topic, directory, delivery) read Link and
// Pairing instead of constructing their own.
type Assembly struct {
	Pairing             *pairing.Pairing
	Link                *link.Link
	Runner              LinkRunner
	LinkProtocolVersion uint32 // 0: linkpb.ProtocolVersion

	// System reports that Run runs the system form. configWiring refuses to
	// fall back to allowAllAccess in that form.
	System bool

	// Access is the configuration-file checker the system assembler builds.
	// In user mode nil means allowAllAccess{}.
	Access accessChecker

	// configAssembled records that a configWiring ran, and effectiveAccess is
	// the checker it installed. Run requires both in system mode, so a missing
	// assembler or a checker other than Access cannot silently allow every
	// caller.
	configAssembled bool
	effectiveAccess accessChecker
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
// and link first, then the topic registry, then the directory, then the
// delivery. configWiring is last: it sees the link and the card issuer the
// earlier assemblers built, and its subscriptions are registered after
// pairing's own broker.alias subscription. The directory sits after the topic
// registry because it reads the registry's public topic set on a public switch
// change, and before configWiring because that one uses the card issuer hook
// the directory adapter installs.
func DefaultAssemblers() []Assembler {
	return []Assembler{newPeerAssembler(), topicAssembler{}, &directoryAssembler{}, newDeliveryAssembler(), &configWiring{}}
}
