package inbox

import (
	"strings"
	"unicode"
)

// NormalizeName returns the name as Claude Code matches session names: control
// and format characters are dropped, surrounding whitespace is trimmed, the
// result is lowercased, and every remaining whitespace character becomes a
// hyphen.
func NormalizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, name)
}
