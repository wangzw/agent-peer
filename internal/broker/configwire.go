package broker

import (
	"log/slog"

	"github.com/wangzw/agent-peer/internal/config"
)

// linkControl is the part of the link package that configuration changes
// drive. The link assembler passes its *link.Link.
type linkControl interface {
	Listen(address string) error
	AnnounceAlias()
	AnnounceAddresses()
}

// agentAddressNotifier is the Broker behaviour that recomputes every local
// agent's address and pushes an agent_changed event on all registered streams.
type agentAddressNotifier interface {
	localAliasChanged()
}

// subscribeConfigChanges registers the callbacks that forward configuration
// changes to link, directory and the local agent table.
//
// Config calls subscribers one at a time, in the order the changes were
// submitted, so the forwarding below runs serially in that same order. The
// alias callback and the advertised-address callback both reissue the card;
// when one change touches both, Reissue runs twice, because config does not
// reveal that the two notifications belong to the same submission.
func subscribeConfigChanges(cfg ConfigSource, l linkControl, d CardIssuer, a agentAddressNotifier, log *slog.Logger) {
	cfg.Subscribe("broker.listen", func(_, new config.Value) {
		if err := l.Listen(new.String); err != nil {
			log.Error("listen_rebind_failed", "address", new.String, "error", err)
		}
	})

	cfg.Subscribe("broker.alias", func(_, _ config.Value) {
		l.AnnounceAlias()
		reissueCard(d)
		a.localAliasChanged()
	})

	cfg.SubscribeAdvertised(func(_, _ []config.AdvertisedAddr) {
		l.AnnounceAddresses()
		reissueCard(d)
	})
}

// reissueCard re-signs the own card. A nil issuer means the Broker runs
// without a directory, so there is nothing to re-sign.
func reissueCard(d CardIssuer) {
	if d != nil {
		d.Reissue()
	}
}
