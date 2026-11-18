package topic

import (
	"crypto/sha256"
	"unicode"
	"unicode/utf8"
)

// ValidateName reports whether name is a legal topic name: nil or ErrInvalidTopic.
// A name is 1 to 256 bytes long, valid UTF-8, and free of control characters.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 256 {
		return ErrInvalidTopic
	}
	if !utf8.ValidString(name) {
		return ErrInvalidTopic
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrInvalidTopic
		}
	}
	return nil
}

// digest returns SHA-256 of the salt followed by the name. It never writes to
// the caller's salt slice.
func digest(salt []byte, name string) [32]byte {
	buf := append(append([]byte{}, salt...), name...)
	return sha256.Sum256(buf)
}
