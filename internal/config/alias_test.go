package config

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hostA", "hostA"},
		{"build-box.example", "build-box.example"},
		{"a-b_c.9", "a-b_c.9"},
		{"host name", "host-name"},
		{"host:name/1", "host-name-1"},
		{"主机", "--"},
		{"a主b", "a-b"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := sanitizeHostname(tc.in); got != tc.want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// REQ-broker-pairing-028: the hostname shown next to a pairing key is the
// system hostname with every character outside the alias character set
// replaced by "-"; an unreadable hostname yields an empty string and a log
// entry rather than failing.
func TestREQBrokerPairing028_HostnameSanitized(t *testing.T) {
	t.Run("sanitizes every illegal character", func(t *testing.T) {
		if got := sanitizeHostname("work station (lab)"); got != "work-station--lab-" {
			t.Fatalf("sanitizeHostname() = %q, want %q", got, "work-station--lab-")
		}
	})

	t.Run("a multi-byte character becomes one dash", func(t *testing.T) {
		if got := sanitizeHostname("主机"); got != "--" {
			t.Fatalf("sanitizeHostname() = %q, want %q", got, "--")
		}
	})

	t.Run("an empty hostname stays empty", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		got := sanitizeHostnameFrom(func() (string, error) { return "", nil }, logger)
		if got != "" {
			t.Fatalf("sanitizeHostnameFrom(empty) = %q, want an empty string", got)
		}
		if buf.Len() != 0 {
			t.Fatalf("sanitizeHostnameFrom(empty) logged %q, want no log entry", buf.String())
		}
	})

	t.Run("an os.Hostname failure logs and returns empty", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		boom := errors.New("hostname unavailable")
		got := sanitizeHostnameFrom(func() (string, error) { return "", boom }, logger)
		if got != "" {
			t.Fatalf("sanitizeHostnameFrom(failure) = %q, want an empty string", got)
		}
		logged := buf.String()
		if !strings.Contains(logged, "hostname") || !strings.Contains(logged, "hostname unavailable") {
			t.Fatalf("log = %q, want it to name the hostname failure", logged)
		}
	})
}

// REQ-broker-pairing-002: with no configured alias, the default alias is the
// sanitized hostname plus "-<listen port>"; without a listener it is the
// hostname alone.
func TestREQBrokerPairing002_DefaultAlias(t *testing.T) {
	listenAll := listenAddr{set: true, all: true, port: 17443}
	listenSpecific := listenAddr{set: true, host: "127.0.0.1", port: 17444}
	tests := []struct {
		name     string
		hostname string
		listen   listenAddr
		want     string
	}{
		{"hostname and port", "hostA", listenAll, "hostA-17443"},
		{"hostname only", "hostA", listenAddr{}, "hostA"},
		{"specific listener uses its port", "hostA", listenSpecific, "hostA-17444"},
		{"empty hostname with port", "", listenAll, "broker-17443"},
		{"empty hostname without listener", "", listenAddr{}, "broker"},
		{"illegal characters are replaced", "host name", listenAll, "host-name-17443"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultAlias(tc.hostname, tc.listen); got != tc.want {
				t.Fatalf("defaultAlias(%q, %+v) = %q, want %q", tc.hostname, tc.listen, got, tc.want)
			}
		})
	}
}

// REQ-broker-pairing-037: the default alias is at most 24 characters. When a
// listener is configured, the "-<port>" suffix is kept and the hostname part
// is shortened to make room for it.
func TestREQBrokerPairing037_AliasTruncation(t *testing.T) {
	longHost := strings.Repeat("a", 30)
	tests := []struct {
		name     string
		hostname string
		listen   listenAddr
		want     string
	}{
		{
			name:     "without a listener the hostname is cut at 24",
			hostname: longHost,
			want:     strings.Repeat("a", 24),
		},
		{
			name:     "with a listener the port suffix survives",
			hostname: longHost,
			listen:   listenAddr{set: true, all: true, port: 17443},
			want:     strings.Repeat("a", 18) + "-17443",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultAlias(tc.hostname, tc.listen)
			if got != tc.want {
				t.Fatalf("defaultAlias() = %q, want %q", got, tc.want)
			}
			if n := len([]rune(got)); n > 24 {
				t.Fatalf("defaultAlias() length = %d, want at most 24: %q", n, got)
			}
		})
	}
}

// REQ-broker-pairing-003: a configured alias must be 1 to 24 characters from
// the alias character set.
func TestREQBrokerPairing003_AliasValidation(t *testing.T) {
	tests := []struct {
		alias   string
		wantErr bool
	}{
		{"a", false},
		{"build-box", false},
		{"a.b-c_d", false},
		{strings.Repeat("a", 24), false},
		{"", true},
		{strings.Repeat("a", 25), true},
		{"build box", true},
		{"主机", true},
		{"bad/name", true},
	}
	for _, tc := range tests {
		err := validAlias(tc.alias)
		if tc.wantErr && err == nil {
			t.Errorf("validAlias(%q) = nil, want an error", tc.alias)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validAlias(%q) = %v, want nil", tc.alias, err)
		}
	}
}

func TestEffectiveAlias(t *testing.T) {
	t.Run("configured alias wins", func(t *testing.T) {
		c := &core{
			explicitAlias: true,
			values:        map[string]Value{"broker.alias": stringValue("build-box")},
		}
		if got := c.effectiveAlias(); got != "build-box" {
			t.Fatalf("effectiveAlias() = %q, want build-box", got)
		}
	})

	t.Run("no configured alias follows the listener", func(t *testing.T) {
		c := &core{
			hostname:        func() (string, error) { return "hostA", nil },
			effectiveListen: listenAddr{set: true, all: true, port: 17443},
			values:          make(map[string]Value),
		}
		if got := c.effectiveAlias(); got != "hostA-17443" {
			t.Fatalf("effectiveAlias() = %q, want hostA-17443", got)
		}
	})

	t.Run("no listener and no hostname falls back to broker", func(t *testing.T) {
		c := &core{
			hostname: func() (string, error) { return "", nil },
			values:   make(map[string]Value),
		}
		if got := c.effectiveAlias(); got != "broker" {
			t.Fatalf("effectiveAlias() = %q, want broker", got)
		}
	})
}
