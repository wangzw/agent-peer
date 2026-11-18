package inbox

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// envelopeTag is the element the transport library wraps an attributed
	// message body in.
	envelopeTag = "cross-session-message"

	// maxFromNameBytes is the receiver's and the transport library's byte limit
	// for a from-name. Deliver rejects a signature longer than this so the
	// library's own truncation never changes it.
	maxFromNameBytes = 200

	// frameSizeMsgID is a canonical 36-byte UUID placeholder. FrameSize counts a
	// fixed-width id so its result does not depend on the message id; the caller
	// passes a canonical UUID to Deliver.
	frameSizeMsgID = "00000000-0000-4000-8000-000000000000"
)

// userFrame mirrors the transport library's userFrame, including field order.
// encoding/json emits fields in declaration order, so the byte count matches the
// library's encoding.
type userFrame struct {
	MsgV      int          `json:"msgV"`
	MsgID     string       `json:"msg_id"`
	Type      string       `json:"type"`
	Message   framePayload `json:"message"`
	Priority  string       `json:"priority"`
	From      string       `json:"from,omitempty"`
	SessionID string       `json:"session_id,omitempty"`
	UUID      string       `json:"uuid,omitempty"`
}

type framePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FrameSize returns the number of bytes this message encodes to, using the same
// JSON encoding the transport library uses. The trailing newline that terminates
// the socket line is not counted, matching the library's own size check.
func FrameSize(sig, text string) int {
	content := text
	if sig != "" {
		content = wrapEnvelope(sig, text)
	}

	encoded, err := json.Marshal(userFrame{
		MsgV:     1,
		MsgID:    frameSizeMsgID,
		Type:     "user",
		Message:  framePayload{Role: "user", Content: content},
		Priority: "next",
	})
	if err != nil {
		// userFrame holds only strings and an int, so json.Marshal cannot fail.
		return 0
	}
	return len(encoded)
}

// wrapEnvelope builds the attribution envelope exactly as the transport library
// does for a message with no reply address. The receiver re-serializes what it
// parses and compares it with the original, so the shape has to match.
func wrapEnvelope(name, body string) string {
	var b strings.Builder
	b.WriteString("<")
	b.WriteString(envelopeTag)
	if clean := sanitizeForEnvelope(name); clean != "" {
		b.WriteString(" from-name=")
		b.WriteString(strconv.Quote(clean))
	}
	b.WriteString(">\n")
	b.WriteString(escapeBody(body))
	b.WriteString("\n</")
	b.WriteString(envelopeTag)
	b.WriteString(">")
	return b.String()
}

// sanitizeForEnvelope mirrors the transport library's sanitizeName.
func sanitizeForEnvelope(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '"', '<', '>', '\n', '\r':
			return -1
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > maxFromNameBytes {
		cut := maxFromNameBytes
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = strings.TrimSpace(name[:cut])
	}
	return name
}

// escapeBody neutralizes closing envelope tags in the body so a message cannot
// forge the end of its own attribution envelope.
func escapeBody(body string) string {
	return strings.ReplaceAll(body, "</"+envelopeTag, `<\/`+envelopeTag)
}
