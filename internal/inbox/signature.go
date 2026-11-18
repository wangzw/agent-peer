package inbox

import (
	"strconv"
	"strings"
)

// maxSignatureRunes is the receiver's rune limit for a from-name.
const maxSignatureRunes = 64

// CleanSignature returns sig as the receiver sees it: quotes, angle brackets,
// backslashes and every code point strconv.IsPrint reports as non-printable are
// dropped, surrounding whitespace is trimmed, and the result is truncated to 64
// runes. The receiver reads the name out of a JSON string, which escapes those
// characters, so keeping any of them would show it a name other than the one
// sent. An empty signature is not a valid from-name; Deliver rejects it rather
// than sending an unattributed message.
func CleanSignature(sig string) string {
	sig = strings.Map(func(r rune) rune {
		switch r {
		case '"', '<', '>', '\\':
			return -1
		}
		if !strconv.IsPrint(r) {
			return -1
		}
		return r
	}, sig)
	sig = strings.TrimSpace(sig)
	if runes := []rune(sig); len(runes) > maxSignatureRunes {
		sig = string(runes[:maxSignatureRunes])
	}
	return sig
}
