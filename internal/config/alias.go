package config

import (
	"fmt"
	"strings"
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

// fallbackAlias is the default alias when no hostname can be read.
const fallbackAlias = "broker"

// defaultAlias derives the default alias for the deployment mode, the hostname
// and the account name. The listen address, the listen port and the advertised
// address play no part.
//
// A system alias is the first label of the hostname. A user alias
// is that label, a dot and the account name. Characters outside the alias
// character set are replaced by '-'. An empty hostname uses fallbackAlias for
// the hostname part, and a user alias still appends the account. An
// empty account name leaves the alias at the hostname part; the caller
// substitutes the uid when the account lookup fails.
//
// The result is at most AliasMaxLen characters. When it is too long, the
// hostname part is shortened first, down to one character; the account part is
// shortened only while the result is still too long. A shortened part never
// ends in "." or "-".
func defaultAlias(mode Mode, hostname, account string) string {
	host := firstLabel(sanitizeHostname(hostname))
	if host == "" {
		host = fallbackAlias
	}
	if mode != ModeUser {
		if got := truncateAliasPart(host, AliasMaxLen); got != "" {
			return got
		}
		return fallbackAlias
	}

	acct := sanitizeHostname(account)
	if acct == "" {
		if got := truncateAliasPart(host, AliasMaxLen); got != "" {
			return got
		}
		return fallbackAlias
	}
	if len([]rune(host))+1+len([]rune(acct)) <= AliasMaxLen {
		return host + "." + acct
	}

	maxHost := AliasMaxLen - 1 - len([]rune(acct))
	if maxHost < 1 {
		maxHost = 1
	}
	host = truncateAliasPart(host, maxHost)
	if host == "" {
		host = fallbackAlias
	}
	maxAcct := AliasMaxLen - 1 - len([]rune(host))
	acct = truncateAliasPart(acct, maxAcct)
	if acct == "" {
		return host
	}
	return host + "." + acct
}

// firstLabel returns the part of the hostname before the first dot.
func firstLabel(host string) string {
	if i := strings.IndexByte(host, '.'); i >= 0 {
		return host[:i]
	}
	return host
}

// truncateAliasPart shortens s to at most max characters, then drops trailing
// dots and dashes from the shortened result.
func truncateAliasPart(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len([]rune(s)) <= max {
		return s
	}
	return strings.TrimRight(truncateRunes(s, max), ".-")
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
