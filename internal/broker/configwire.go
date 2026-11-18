package broker

import (
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/link"
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
// changes to link, directory and the local agent table. When stopped is set,
// each callback returns without forwarding: a change config delivers during
// shutdown must not reach a listener that is already closing or log its
// failure.
//
// Config calls subscribers one at a time, in the order the changes were
// submitted, so the forwarding below runs serially in that same order. The
// alias callback and the advertised-address callback both reissue the card;
// when one change touches both, Reissue runs twice, because config does not
// reveal that the two notifications belong to the same submission.
func subscribeConfigChanges(cfg ConfigSource, l linkControl, d CardIssuer, a agentAddressNotifier, log *slog.Logger, stopped *atomic.Bool) {
	cfg.Subscribe("broker.listen", func(_, new config.Value) {
		if stopped.Load() {
			return
		}
		if err := l.Listen(new.String); err != nil {
			logListenRebindFailure(log, new.String, err)
		}
	})

	cfg.Subscribe("broker.alias", func(_, _ config.Value) {
		if stopped.Load() {
			return
		}
		l.AnnounceAlias()
		reissueCard(d)
		a.localAliasChanged()
	})

	cfg.SubscribeAdvertised(func(_, _ []config.AdvertisedAddr) {
		if stopped.Load() {
			return
		}
		l.AnnounceAddresses()
		reissueCard(d)
	})
}

// logListenRebindFailure records a failed broker.listen rebind. A link closed
// by shutdown answers with link.ErrClosed; that is the expected outcome of a
// change delivered between close and the components stopping, so it is logged
// at debug instead of as an error. Every other failure is an error.
func logListenRebindFailure(log *slog.Logger, address string, err error) {
	if errors.Is(err, link.ErrClosed) {
		log.Debug("listen_rebind_failed", "address", address, "error", err)
		return
	}
	log.Error("listen_rebind_failed", "address", address, "error", err)
}

// reissueCard re-signs the own card. A nil issuer means the Broker runs
// without a directory, so there is nothing to re-sign.
func reissueCard(d CardIssuer) {
	if d != nil {
		d.Reissue()
	}
}
