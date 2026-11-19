package broker

import (
	"strings"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/inbox"
)

// maxInboxFrameBytes is the encoded frame size a send may not exceed. A message
// whose frame is larger is rejected with too_large before any request is sent
// (REQ-agent-messaging-026).
const maxInboxFrameBytes = 1_000_000

// signatureFor returns the address of an Agent as a viewer whose alias for the
// Agent's Broker is alias sees it (design-broker.md:258). ok is false when no
// valid signature exists: the cleaned name is empty, ref is not 6 to 12
// lowercase hexadecimal characters, or alias is malformed.
//
// The spelling is FormatAddress's, so the signature and the address the Broker
// lists for the same Agent follow one rule.
func signatureFor(name, ref string, unique bool, alias string) (sig string, ok bool) {
	if inbox.CleanSignature(name) == "" {
		return "", false
	}
	if !ValidAlias(alias) {
		return "", false
	}
	if !validRef(ref) {
		return "", false
	}
	s := FormatAddress(name, ref, alias, !unique)
	// FormatAddress trims and truncates, but a signature that the receiver would
	// rewrite is not a signature: the receiver would show a different sender.
	if inbox.CleanSignature(s) != s {
		return "", false
	}
	return s, true
}

// MaxSendFrameSize returns the encoded frame size of text under the longest
// signature a sender named name with ref could get on any receiver. Send
// rejects a message with too_large when the result exceeds 1 000 000
// (REQ-agent-messaging-026).
//
// The sender knows every part of its signature except the alias the receiver
// uses for its Broker, so every alias length from 1 to config.AliasMaxLen is
// tried, with and without the ref. The longest signature is chosen by comparing
// frame sizes with a short body: JSON escaping is per character, so the body
// contributes the same amount to every candidate.
func MaxSendFrameSize(name, ref, text string) int {
	best := ""
	bestSize := -1
	for aliasLen := 1; aliasLen <= config.AliasMaxLen; aliasLen++ {
		alias := strings.Repeat("a", aliasLen)
		for _, unique := range []bool{true, false} {
			sig, ok := signatureFor(name, ref, unique, alias)
			if !ok {
				continue
			}
			if n := inbox.FrameSize(sig, "x"); n > bestSize {
				bestSize = n
				best = sig
			}
		}
	}
	if bestSize < 0 {
		return inbox.FrameSize("", text)
	}
	return inbox.FrameSize(best, text)
}
