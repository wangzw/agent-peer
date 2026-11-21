package link

import (
	"crypto/tls"
	"log/slog"
	"time"

	"github.com/wangzw/peers/internal/config"
	"github.com/wangzw/peers/internal/pairing"
)

// Peers is the part of the peer table link depends on. *pairing.Pairing
// implements it.
type Peers interface {
	Fingerprint() pairing.Fingerprint
	TLSConfig() *tls.Config
	Peers() []pairing.Peer
	Role(fp pairing.Fingerprint) pairing.Role
	EffectiveAlias(fp pairing.Fingerprint) string
	Candidates(fp pairing.Fingerprint) (addresses []string, last string)
	ObserveAlias(fp pairing.Fingerprint, alias string)
	ObserveAcceptorAddresses(fp pairing.Fingerprint, addresses []config.AdvertisedAddr)
	ObserveConnected(fp pairing.Fingerprint, address string)
	Accept(conn pairing.PairConn) error
}

// Settings is the part of the configuration link depends on. ConfigSettings
// implements it.
type Settings interface {
	Alias() string
	Advertised() []config.AdvertisedAddr
	DialTimeout() time.Duration
	ReconnectMin() time.Duration
	ReconnectMax() time.Duration
	KeepaliveInterval() time.Duration
	KeepaliveTimeout() time.Duration
	Audit(e config.Event)
}

// Clock is the time source link uses. RealClock implements it, and test fakes
// can implement it without importing link.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

// Options configures New.
type Options struct {
	// Peers is the peer table; required.
	Peers Peers
	// Settings is the configuration view; required.
	Settings Settings
	// Clock is the time source; real time when nil.
	Clock Clock
	// Logger receives diagnostic logs; slog.Default when nil.
	Logger *slog.Logger
	// ProtocolVersion is the link protocol version advertised in Hello and
	// compared on receipt. Zero means linkpb.ProtocolVersion.
	ProtocolVersion uint32
}
