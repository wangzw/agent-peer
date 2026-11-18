package config

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// sanitizeHostname replaces every character outside the alias character set
// with '-'. A multi-byte character counts as one character and becomes one '-'.
func sanitizeHostname(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			r = '-'
		}
		out = append(out, r)
	}
	return string(out)
}

func validAlias(s string) error {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 24 {
		return fmt.Errorf("length must be between 1 and 24 characters, got %d", n)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return fmt.Errorf("character %q is not allowed", r)
		}
	}
	return nil
}

// defaultAlias derives the alias from the hostname and the effective listen
// address. An unset listener yields up to 24 characters of the hostname; a set
// listener appends "-<port>" and truncates the hostname part so the result is
// at most 24 characters.
func defaultAlias(hostname string, listen listenAddr) string {
	host := sanitizeHostname(hostname)
	if !listen.set {
		if host == "" {
			return "broker"
		}
		return truncateRunes(host, 24)
	}
	suffix := "-" + strconv.Itoa(listen.port)
	maxHost := 24 - len(suffix)
	if maxHost < 0 {
		maxHost = 0
	}
	host = truncateRunes(host, maxHost)
	if host == "" {
		host = "broker"
	}
	return host + suffix
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
