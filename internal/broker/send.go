package broker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// Send runs one send for caller. The returned error is nil or a *Error.
//
// The order is deliberate: the body, then the longest-signature frame size,
// then the address. An oversized message must be rejected with too_large before
// any request leaves this Broker and before any inbox write is attempted
// (REQ-agent-messaging-026). A parsed alias that names this Broker takes the
// local path; every other alias must resolve to a paired peer.
func (d *delivery) Send(ctx context.Context, caller agentView, to, text string) (err error) {
	start := time.Now()
	cfg := d.deps.Settings.Send()
	deadline := start.Add(cfg.RequestTimeout)
	alias := d.deps.Settings.LocalAlias()

	// The log line is written whatever the result, including a validation
	// failure that never produced a message id or a parsed target.
	msgID := ""
	target := to
	defer func() {
		d.logSend(caller, target, msgID, err, time.Since(start))
	}()

	if text == "" {
		return notSent(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "message is empty")
	}

	msgID = d.messageID()

	size := MaxSendFrameSize(caller.Name, caller.Ref, text)
	if size > maxInboxFrameBytes {
		return notSent(localpb.ErrorCode_ERROR_CODE_TOO_LARGE,
			"message encodes to %d bytes with the longest possible signature; the limit is %d",
			size, maxInboxFrameBytes)
	}

	addr, ok := parseTarget(to)
	if !ok {
		return notSent(localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"%q is not an agent address of the form name@alias or name [ref]@alias", to)
	}
	target = addr.Name

	if addr.Alias == alias {
		return d.sendLocal(ctx, caller, addr, alias, text, msgID, deadline)
	}

	peer, ok := d.deps.Peers.ResolveAlias(addr.Alias)
	if !ok {
		return notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "no Broker is known as %q", addr.Alias)
	}
	if d.toPeer == nil {
		return notSent(localpb.ErrorCode_ERROR_CODE_INTERNAL, "remote delivery is not wired")
	}
	return d.toPeer(ctx, peerSend{
		Caller:    caller,
		Peer:      peer,
		Addr:      addr,
		MessageID: msgID,
		Text:      text,
		Deadline:  deadline,
	})
}

// sendLocal resolves addr among the local Agents the caller can see and writes
// the message to the target's own inbox backend.
func (d *delivery) sendLocal(ctx context.Context, caller agentView, addr parsedAddress, alias, text, msgID string, deadline time.Time) error {
	all := d.deps.Agents.All()
	cands := localCandidates(all,
		func(a agentView) bool {
			return d.deps.Topics.VisibleLocal(caller.ID, a.ID)
		},
		func(name, ref string, unique bool) string {
			// A candidate with no valid signature is still listed by name; only
			// the matched target needs a usable signature below.
			sig, _ := signatureFor(name, ref, unique, alias)
			return sig
		})

	idx, perr := pickTarget(addr, cands, localRefMatch, "on "+alias)
	if perr != nil {
		return perr
	}
	name := cands[idx].Name

	target, err := d.deps.Agents.Reread(cands[idx].ID)
	if err != nil {
		if errors.Is(err, errAgentGone) {
			return notSent(localpb.ErrorCode_ERROR_CODE_INBOX_UNWRITABLE, "%s has ended", name)
		}
		return notSent(localpb.ErrorCode_ERROR_CODE_INBOX_UNWRITABLE, "%s could not be reread: %v", name, err)
	}

	// Uniqueness is judged over the whole local table, not only the Agents the
	// caller can see, so a duplicate elsewhere still forces the ref into the
	// signature.
	counts := make(map[string]int, len(all))
	for _, a := range all {
		counts[a.Name]++
	}
	sig, ok := signatureFor(caller.Name, caller.Ref, counts[caller.Name] == 1, alias)
	if !ok {
		// The caller's own name and ref come from this Broker, so a failure here
		// is a local defect, not a bad address.
		return notSent(localpb.ErrorCode_ERROR_CODE_INTERNAL, "cannot build a signature for %q", caller.Name)
	}

	switch d.writeInbox(ctx, deadline, target, sig, text, msgID) {
	case written:
		return nil
	case uncertain:
		return unknownResult("%s may have received the message", name)
	default:
		return notSent(localpb.ErrorCode_ERROR_CODE_INBOX_UNWRITABLE, "%s did not accept the message", name)
	}
}

// messageID returns a new message identifier, using the injected generator when
// a test supplies one.
func (d *delivery) messageID() string {
	if d.deps.NewID != nil {
		return d.deps.NewID()
	}
	return newMessageID()
}

// logSend records one send without the body: the receiver's identity, the
// message id and the outcome (architecture.md:255).
func (d *delivery) logSend(caller agentView, target, msgID string, err error, elapsed time.Duration) {
	logger := d.deps.Log
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("send",
		"message_id", msgID,
		"sender", caller.Name,
		"target", target,
		"result", sendResultName(err),
		"elapsed_ms", elapsed.Milliseconds(),
	)
}

// sendResultName renders a send outcome for the log: delivered, or the
// lower-case wire code name of the error.
func sendResultName(err error) string {
	if err == nil {
		return "delivered"
	}
	var e *Error
	if errors.As(err, &e) {
		name := strings.TrimPrefix(e.Code.String(), "ERROR_CODE_")
		return strings.ToLower(name)
	}
	return "internal"
}
