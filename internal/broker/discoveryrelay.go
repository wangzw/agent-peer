package broker

import (
	"errors"
	"log/slog"
	"sort"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/directory"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// publicSwitch is the part of the directory the public switch relay drives:
// the broker's own public flag and its public topic declarations.
type publicSwitch interface {
	SetPublic(public bool) error
	DeclarePublicTopic(name, description string) error
	RetirePublicTopic(name string)
}

// cardPairing is the part of the peer table that accepts the advertised
// addresses carried by a directory card.
type cardPairing interface {
	Role(fp pairing.Fingerprint) pairing.Role
	UpdateCardAddresses(fp pairing.Fingerprint, addrs []config.AdvertisedAddr)
}

// publicChanged applies a new value of broker.public to the directory.
//
// Becoming public re-declares every topic this broker had declared publicly
// before. The set is snapshotted first and read again after the declarations;
// topics that left it in between are retired, so a declaration is never left
// behind by a topic that was dropped while the broker was private. Becoming
// private only clears the public flag; the directory withdraws the broker's
// card and announcements on its own.
func publicChanged(sw publicSwitch, topics func() map[string]string, public bool, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if err := sw.SetPublic(public); err != nil {
		logSetPublicFailure(log, public, err)
		return
	}
	if !public {
		return
	}

	snapshot := topics()
	names := make([]string, 0, len(snapshot))
	for name := range snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := sw.DeclarePublicTopic(name, snapshot[name]); err != nil {
			log.Warn("public_topic_redeclare_failed", "topic", name, "error", err)
		}
	}

	current := topics()
	var retired []string
	for name := range snapshot {
		if _, ok := current[name]; !ok {
			retired = append(retired, name)
		}
	}
	sort.Strings(retired)
	for _, name := range retired {
		sw.RetirePublicTopic(name)
	}
}

// logSetPublicFailure reports a failed SetPublic. ErrClosed means the directory
// was already closed, which happens when a queued configuration notification
// arrives during shutdown; that is expected and logged at debug level.
func logSetPublicFailure(log *slog.Logger, public bool, err error) {
	if errors.Is(err, directory.ErrClosed) {
		log.Debug("set_public_closed", "public", public, "error", err)
		return
	}
	log.Warn("set_public_failed", "public", public, "error", err)
}

// relayCard hands the addresses carried by a directory card to the peer table.
// Only a card published by a peer this broker dials is recorded; cards from
// acceptor peers and from non-peers are ignored.
func relayCard(p cardPairing, fp pairing.Fingerprint, addrs []config.AdvertisedAddr) {
	if p.Role(fp) != pairing.RoleDialer {
		return
	}
	p.UpdateCardAddresses(fp, addrs)
}
