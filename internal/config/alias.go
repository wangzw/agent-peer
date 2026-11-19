package config

import (
	"fmt"
	"strconv"
)

// AliasMaxLen is the maximum length of a Broker alias in bytes. It is the one
// definition of the limit: the configuration validator, pairing and the Broker
// all measure aliases against it.
const AliasMaxLen = 24

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
	n := len(s)
	if n < 1 {
		return fmt.Errorf("length must be between 1 and %d characters, got %d", AliasMaxLen, n)
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
	if n > AliasMaxLen {
		return fmt.Errorf("length is longer than %d characters, got %d", AliasMaxLen, n)
	}
	return nil
}

// defaultAlias derives the alias from the hostname and the effective listen
// address. An unset listener yields up to AliasMaxLen characters of the
// hostname; a set listener appends "-<port>" and truncates the hostname part so
// the result is at most AliasMaxLen characters.
func defaultAlias(hostname string, listen listenAddr) string {
	host := sanitizeHostname(hostname)
	if !listen.set {
		if host == "" {
			return "broker"
		}
		return truncateRunes(host, AliasMaxLen)
	}
	suffix := "-" + strconv.Itoa(listen.port)
	maxHost := AliasMaxLen - len(suffix)
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
