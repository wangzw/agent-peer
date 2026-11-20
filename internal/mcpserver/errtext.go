package mcpserver

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/status"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// callKind selects the opening phrase of the text shown to the model.
type callKind int

const (
	callGeneric callKind = iota
	callSend
	callConnect
	callUpdate
)

// permNeed is the config-file permission a tool call requires.
type permNeed int

const (
	permNone permNeed = iota
	permRead
	permWrite
)

// Texts shown to the model. The local-state texts are format strings; the rest
// are used verbatim.
const (
	textStarting        = "the connection to the Broker is still being set up; retry shortly"
	textUnavailable     = "the Broker is unavailable: %s; retrying in the background"
	textNoBroker        = "no Broker is available for this account; if a system-level Broker is installed on this host, ask the administrator to start it. Otherwise start a user-level Broker with `agent-peer service install`, or run `agent-peer broker` in a terminal outside the Claude Code session (a Broker started inside the session exits with code 6)"
	textNoSystemBroker  = "the system-level Broker is unavailable; ask the administrator to start it"
	textVersionMismatch = "%s. To fix it, %s"
	textNotRegistered   = "this session is not registered as an agent: %s"

	textSendLost    = "lost contact with the Broker before it answered; the message may have been delivered, and sending it again may deliver it twice"
	textConnectLost = "lost contact with the Broker before it answered; the pairing may still have completed, check list_brokers"
	textGenericLost = "the Broker did not answer: %s"
	textPeerOffline = "the other Broker is offline and refuses messages from other hosts; sending the same message again will not deliver it. Tell the other user by another means if you can."

	textNeedRead  = "The calling account needs read permission on the Broker's config file"
	textNeedWrite = "The calling account needs write permission on the Broker's config file"

	textNoDefaultTopic = "this session has no default topic; pass name to choose one"
)

// errNoBroker is the selection result when no Broker socket answers. Its text
// names the two ways to start a Broker, so the model can tell the user.
var errNoBroker = errors.New(textNoBroker)

// openingPhrase returns the phrase that starts the error text for a call kind.
func openingPhrase(tool string, kind callKind) string {
	switch kind {
	case callSend:
		return "Not sent"
	case callConnect:
		return "Not connected"
	case callUpdate:
		return textUpdateFailed
	default:
		return tool + " failed"
	}
}

// openingPhraseForCode returns the phrase for a Broker error. The phrase
// follows the outcome the error code describes: a delivery_unknown result is
// unknown whichever tool asked, and every other outcome follows the call kind.
func openingPhraseForCode(tool string, kind callKind, code localpb.ErrorCode) string {
	if code == localpb.ErrorCode_ERROR_CODE_DELIVERY_UNKNOWN {
		return "Delivery unknown"
	}
	return openingPhrase(tool, kind)
}

// toolError builds "<phrase>: <body> (code: <name>)".
func toolError(phrase, body string, code localpb.ErrorCode) error {
	return fmt.Errorf("%s: %s (code: %s)", phrase, body, codeName(code))
}

// notRegisteredReason turns a not_registered event reason into the sentence
// shown to the model. A non-empty detail from the Broker names the specific
// cause and wins over the general sentence for the reason.
func notRegisteredReason(r localpb.NotRegisteredReason, detail string) string {
	if detail != "" {
		return detail
	}
	switch r {
	case localpb.NotRegisteredReason_NOT_REGISTERED_REASON_SESSION_RECORD_NOT_FOUND:
		return "no Claude Code session record matches this session's inbox socket"
	case localpb.NotRegisteredReason_NOT_REGISTERED_REASON_NAME_UNUSABLE:
		return "the session name is unusable (empty, or contains @ or *); rename the session"
	case localpb.NotRegisteredReason_NOT_REGISTERED_REASON_SOCKET_NOT_OWNED:
		return "the inbox socket is not owned by this account"
	case localpb.NotRegisteredReason_NOT_REGISTERED_REASON_SOCKET_PATH_MISSING:
		return "the session had no inbox socket when this MCP server started; reconnect the MCP server with /mcp"
	case localpb.NotRegisteredReason_NOT_REGISTERED_REASON_BROKER_DEGRADED:
		return "the Broker runs degraded with diagnostics only; it cannot serve this call until the startup failure is fixed"
	default:
		return "registration is still in progress"
	}
}

// rpcError turns an error from a Local RPC into the error shown to the model.
func rpcError(tool string, kind callKind, perm permNeed, err error) error {
	d := errorDetail(err)
	if d == nil {
		switch kind {
		case callSend:
			return toolError("Delivery unknown", textSendLost, localpb.ErrorCode_ERROR_CODE_DELIVERY_UNKNOWN)
		case callConnect:
			return toolError("Not connected", textConnectLost, localpb.ErrorCode_ERROR_CODE_INTERNAL)
		default:
			return toolError(openingPhrase(tool, kind), fmt.Sprintf(textGenericLost, rpcMessage(err)), localpb.ErrorCode_ERROR_CODE_INTERNAL)
		}
	}

	phrase := openingPhraseForCode(tool, kind, d.GetCode())
	body := phrase + ": " + stripResultPrefix(d.GetMessage())
	out := body + " (code: " + codeName(d.GetCode()) + ")"
	if fp := d.GetOriginatingBrokerFingerprint(); len(fp) > 0 {
		out += " (reported by Broker " + hex.EncodeToString(fp) + ")"
	}
	switch {
	case d.GetCode() == localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED && perm == permRead:
		out += ". " + textNeedRead
	case d.GetCode() == localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED && perm == permWrite:
		out += ". " + textNeedWrite
	case d.GetCode() == localpb.ErrorCode_ERROR_CODE_BROKER_OFFLINE:
		out += ". " + textPeerOffline
	}
	return errors.New(out)
}

// stripResultPrefix removes a result phrase the Broker already put on a
// message. The phrase rpcError adds is chosen from the error code, so a message
// that names a different outcome must not keep its own phrase.
func stripResultPrefix(message string) string {
	switch {
	case strings.HasPrefix(message, "Not sent: "):
		return strings.TrimPrefix(message, "Not sent: ")
	case strings.HasPrefix(message, "Delivery unknown: "):
		return strings.TrimPrefix(message, "Delivery unknown: ")
	case strings.HasPrefix(message, "Not connected: "):
		return strings.TrimPrefix(message, "Not connected: ")
	}
	return message
}

// errorDetail returns the ErrorDetail carried in the gRPC status details, or
// nil when there is none.
func errorDetail(err error) *localpb.ErrorDetail {
	s, _ := status.FromError(err)
	if s == nil {
		return nil
	}
	for _, detail := range s.Details() {
		if d, ok := detail.(*localpb.ErrorDetail); ok {
			return d
		}
	}
	return nil
}

// rpcMessage returns the gRPC status message of err, which falls back to the
// error text when err carries no status.
func rpcMessage(err error) string {
	return status.Convert(err).Message()
}

// codeName returns the lower-case name of a stable error code. UNSPECIFIED and
// values outside the closed set are reported as internal.
func codeName(c localpb.ErrorCode) string {
	switch c {
	case localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT:
		return "invalid_argument"
	case localpb.ErrorCode_ERROR_CODE_TOO_LARGE:
		return "too_large"
	case localpb.ErrorCode_ERROR_CODE_NOT_FOUND:
		return "not_found"
	case localpb.ErrorCode_ERROR_CODE_AMBIGUOUS:
		return "ambiguous"
	case localpb.ErrorCode_ERROR_CODE_NOT_VISIBLE:
		return "not_visible"
	case localpb.ErrorCode_ERROR_CODE_PEER_UNREACHABLE:
		return "peer_unreachable"
	case localpb.ErrorCode_ERROR_CODE_INBOX_UNWRITABLE:
		return "inbox_unwritable"
	case localpb.ErrorCode_ERROR_CODE_DELIVERY_UNKNOWN:
		return "delivery_unknown"
	case localpb.ErrorCode_ERROR_CODE_PAIRING_REJECTED:
		return "pairing_rejected"
	case localpb.ErrorCode_ERROR_CODE_NEEDS_LOCAL_ALIAS:
		return "needs_local_alias"
	case localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED:
		return "permission_denied"
	case localpb.ErrorCode_ERROR_CODE_NOT_REGISTERED:
		return "not_registered"
	case localpb.ErrorCode_ERROR_CODE_VERSION_MISMATCH:
		return "version_mismatch"
	case localpb.ErrorCode_ERROR_CODE_QUOTA_EXCEEDED:
		return "quota_exceeded"
	case localpb.ErrorCode_ERROR_CODE_INTERNAL:
		return "internal"
	case localpb.ErrorCode_ERROR_CODE_BROKER_OFFLINE:
		return "broker_offline"
	case localpb.ErrorCode_ERROR_CODE_BROKER_DEGRADED:
		return "broker_degraded"
	default:
		return "internal"
	}
}
