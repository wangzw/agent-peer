package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/inbox"
	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// peerID identifies a paired peer Broker.
type peerID = pairing.Fingerprint

// agentView is a read-only snapshot of one local Agent.
type agentView struct {
	ID       string        // 64 lowercase hex chars, SHA-256 of "session:"+socket path
	Name     string        // normalized session name
	Ref      string        // current disambiguation ref, 6..12 lowercase hex, prefix of ID
	PrevName string        // previous name inside the 10-minute window, else ""
	Session  inbox.Session // the record last read
	Inbox    inboxWriter   // the account's session backend; writes go through it
}

// localAgents is the local Agent table a delivery reads. *Broker implements it.
type localAgents interface {
	Lookup(id string) (agentView, bool)
	All() []agentView
	// Reread rereads the record, applies it to the table and returns the new
	// snapshot; errAgentGone (declared by broker-local) when the Agent is gone.
	Reread(id string) (agentView, error)
}

// remoteMember is one member entry a peer reports.
type remoteMember struct{ Name, Ref, PrevName string }

// visibility decides which Agents know each other. topicVisibility implements
// it over the topic table in production.
type visibility interface {
	VisibleLocal(a, b string) bool // two local Agent IDs
	VisibleRemote(local string, peer peerID, name, ref string) bool
	RemoteMembers(peer peerID) []remoteMember // entries in the current intersection
}

// peerTable is the part of the pairing peer table a delivery reads.
type peerTable interface {
	ResolveAlias(alias string) (peerID, bool) // committed peers only
	EffectiveAlias(p peerID) string
	Role(p peerID) pairing.Role
}

// deliveryLink is the part of the link a delivery uses. linkAdapter implements
// it in production.
type deliveryLink interface {
	Request(ctx context.Context, p peerID, f link.Frame) (reply link.Frame, sent bool, err error)
	HandleRequest(kind link.FrameKind, h func(ctx context.Context, p peerID, f link.Frame) link.Frame)
	Subscribe(onUp func(p peerID, salt []byte), onDown func(p peerID))
	Established(p peerID) bool // State(p).Phase == link.PhaseEstablished
	Kick(p peerID)
}

// inboxWriter writes one message to an account's session backend.
// SessionBackend implements it.
type inboxWriter interface {
	Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error
}

// sendSettings is the snapshot of the send.* configuration one operation reads
// once.
type sendSettings struct {
	RequestTimeout, LinkBackoff, InboxBackoff, InboxTimeout, DedupeWindow time.Duration
	LinkRetries, InboxRetries                                             int
}

// settingsSource supplies the send configuration and the local alias.
type settingsSource interface {
	Send() sendSettings
	LocalAlias() string
}

// deliveryDeps is everything a delivery uses. The fields are interfaces so the
// unit tests can substitute fakes.
type deliveryDeps struct {
	Agents   localAgents
	Topics   visibility
	Peers    peerTable
	Link     deliveryLink
	Settings settingsSource
	Clock    func() time.Time // dedupe expiry only
	NewID    func() string    // nil means newMessageID
	Log      *slog.Logger
}

// peerSend is one outgoing message to a remote peer.
type peerSend struct {
	Caller    agentView
	Peer      peerID
	MessageID string
	Text      string
	Deadline  time.Time
}

// delivery implements sending and receiving messages. Broker assembles one
// instance and the Local Send RPC and the link request handler share it.
type delivery struct {
	deps   deliveryDeps
	toPeer func(ctx context.Context, r peerSend) error

	dedupe *dedupeTable
}

// newDelivery builds a delivery around deps. It only stores the dependencies;
// the send path is attached later by the assembly.
func newDelivery(deps deliveryDeps) *delivery {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	d := &delivery{deps: deps}
	d.dedupe = newDedupeTable(clock, func() time.Duration {
		return d.deps.Settings.Send().DedupeWindow
	})
	return d
}

// notSent returns an error for a message that was certainly not delivered. The
// fixed prefix distinguishes it from an unknown result in the message the model
// reads.
func notSent(code localpb.ErrorCode, format string, args ...any) *Error {
	return Errorf(code, "Not sent: "+format, args...)
}

// unknownResult returns an error for a message whose outcome cannot be
// determined. It must stay distinct from notSent (REQ-agent-messaging-027).
func unknownResult(format string, args ...any) *Error {
	return Errorf(localpb.ErrorCode_ERROR_CODE_DELIVERY_UNKNOWN, "Delivery unknown: "+format, args...)
}

// inboxPackage implements inboxWriter with inbox.Deliver. Tests use it as the
// Inbox of agents backed by fakesession.
type inboxPackage struct{}

func (inboxPackage) Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error {
	return inbox.Deliver(ctx, s, sig, text, msgID, timeout)
}

var _ inboxWriter = inboxPackage{}
