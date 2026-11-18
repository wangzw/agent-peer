package pairing

import (
	"net"
	"time"

	"github.com/wangzw/agent-peer/internal/config"
)

// Audit reasons. Detail carries only the reason; it never carries a key, an
// address or any other secret.
const (
	auditReasonKeyInvalid         = "key invalid"
	auditReasonVerificationFailed = "verification failed"
	auditReasonTryLater           = "try later"
	auditReasonTimeout            = "handshake timeout"
	auditReasonNotConnected       = "not connected"
)

// auditPairingSucceeded records a pairing completed on this side. pairing
// writes this event for the acceptor; the broker writes the initiator's event
// with the caller's agent_id after Connect returns.
func auditPairingSucceeded(s settings, now time.Time, fp Fingerprint, alias string) {
	s.Audit(newPairingAuditEvent(config.EventPairingSucceeded, now, fp, alias, "", ""))
}

// auditPairingFailed records a pairing this side rejected or lost. reason is one
// of the auditReason constants. mergeKey is set only for a rate-limited failure,
// where the audit log merges repeated events from the same source.
func auditPairingFailed(s settings, now time.Time, fp Fingerprint, alias, reason, mergeKey string) {
	s.Audit(newPairingAuditEvent(config.EventPairingFailed, now, fp, alias, reason, mergeKey))
}

// auditLocalAliasAssigned records that pairing generated a local alias to
// disambiguate a peer whose self-reported alias collided.
func auditLocalAliasAssigned(s settings, now time.Time, fp Fingerprint, alias string) {
	s.Audit(newPairingAuditEvent(config.EventLocalAliasAssigned, now, fp, alias, "", ""))
}

// newPairingAuditEvent builds one pairing-owned event. Time comes from the
// caller's injected clock, never from time.Now, so tests can pin it. pairing
// leaves Actor and Account empty: those identify a management call and belong
// to the broker.
func newPairingAuditEvent(kind config.EventKind, now time.Time, fp Fingerprint, alias, detail, mergeKey string) config.Event {
	e := config.Event{
		Time:     now,
		Event:    kind,
		Detail:   detail,
		MergeKey: mergeKey,
	}
	var zero Fingerprint
	if fp != zero {
		e.Peer = &config.PeerRef{Fingerprint: [32]byte(fp), Alias: alias}
	}
	return e
}

// auditSourceMergeKey builds the merge key for a rate-limited pairing failure.
// The host is taken from the peer's RemoteAddr so repeated attempts from one
// source collapse in the audit read; the port is deliberately ignored. When the
// address cannot be split, the whole string is the host. An empty address yields
// no merge key, because grouping unrelated events under "source:" would merge
// failures that have no source in common.
func auditSourceMergeKey(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if host == "" {
		return ""
	}
	return "source:" + host
}
