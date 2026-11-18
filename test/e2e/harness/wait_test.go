package harness

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// recordTB captures Fatalf calls so a test can inspect the failure message
// without ending the process. Fatalf panics with fatalStop to unwind the code
// under test the way the real testing.T would.
type recordTB struct {
	testing.TB // always nil; embedded for the unexported method only

	mu     sync.Mutex
	fatals []string
}

type fatalStop struct{}

func (r *recordTB) Helper() {}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	panic(fatalStop{})
}

func (r *recordTB) lastFatal() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fatals) == 0 {
		return ""
	}
	return r.fatals[len(r.fatals)-1]
}

// captureFatal runs fn and returns the message of the Fatalf it raised. A
// different panic is re-raised so a genuine bug is not swallowed.
func captureFatal(t *testing.T, tb *recordTB, fn func()) string {
	t.Helper()
	var msg string
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if _, ok := r.(fatalStop); !ok {
				panic(r)
			}
			msg = tb.lastFatal()
		}()
		fn()
	}()
	return msg
}

func TestEventuallyReportsLastState(t *testing.T) {
	tb := &recordTB{}
	bound := 200 * time.Millisecond

	calls := 0
	start := time.Now()
	msg := captureFatal(t, tb, func() {
		Eventually(tb, bound, "example condition", func() (bool, string) {
			calls++
			return false, fmt.Sprintf("state-%d", calls)
		})
	})
	elapsed := time.Since(start)

	if msg == "" {
		t.Fatal("Eventually returned without reporting a failure")
	}
	last := fmt.Sprintf("state-%d", calls)
	if !strings.Contains(msg, last) {
		t.Fatalf("failure message %q does not contain the last state %q", msg, last)
	}
	if elapsed < bound {
		t.Fatalf("Eventually returned after %s, want at least the bound %s", elapsed, bound)
	}
}

func TestConsistentlyFailsOnFirstViolation(t *testing.T) {
	tb := &recordTB{}
	window := 2 * time.Second

	calls := 0
	start := time.Now()
	msg := captureFatal(t, tb, func() {
		Consistently(tb, window, "steady condition", func() (bool, string) {
			calls++
			return calls != 3, fmt.Sprintf("state-%d", calls)
		})
	})
	elapsed := time.Since(start)

	if msg == "" {
		t.Fatal("Consistently returned without reporting a failure")
	}
	if calls != 3 {
		t.Fatalf("condition ran %d times, want it to stop at the third violation", calls)
	}
	if !strings.Contains(msg, "state-3") {
		t.Fatalf("failure message %q does not contain the state at the violation", msg)
	}
	if elapsed >= window {
		t.Fatalf("Consistently returned after %s, want less than the window %s", elapsed, window)
	}
}

func TestConfigYAMLMerge(t *testing.T) {
	merged := ConfigYAML(FastTimers, "send:\n  request_timeout: 10s\n")

	var cfg struct {
		Link struct {
			ReconnectMin string `yaml:"reconnect_min"`
			ReconnectMax string `yaml:"reconnect_max"`
			DialTimeout  string `yaml:"dial_timeout"`
		} `yaml:"link"`
		Send struct {
			LinkBackoff    string `yaml:"link_backoff"`
			RequestTimeout string `yaml:"request_timeout"`
		} `yaml:"send"`
		Broker struct {
			AddressCheckInterval string `yaml:"address_check_interval"`
		} `yaml:"broker"`
	}
	if err := yaml.Unmarshal([]byte(merged), &cfg); err != nil {
		t.Fatalf("merged configuration is not valid YAML: %v\n%s", err, merged)
	}

	if cfg.Send.RequestTimeout != "10s" {
		t.Fatalf("send.request_timeout = %q, want the later fragment to win with %q", cfg.Send.RequestTimeout, "10s")
	}
	if cfg.Link.ReconnectMin != "100ms" {
		t.Fatalf("link.reconnect_min = %q, want %q", cfg.Link.ReconnectMin, "100ms")
	}
	if cfg.Link.ReconnectMax != "400ms" {
		t.Fatalf("link.reconnect_max = %q, want %q", cfg.Link.ReconnectMax, "400ms")
	}
	if cfg.Link.DialTimeout != "500ms" {
		t.Fatalf("link.dial_timeout = %q, want %q", cfg.Link.DialTimeout, "500ms")
	}
	if cfg.Send.LinkBackoff != "100ms" {
		t.Fatalf("send.link_backoff = %q, want %q", cfg.Send.LinkBackoff, "100ms")
	}
	if cfg.Broker.AddressCheckInterval != "500ms" {
		t.Fatalf("broker.address_check_interval = %q, want %q", cfg.Broker.AddressCheckInterval, "500ms")
	}

	if merged != ConfigYAML(FastTimers, "send:\n  request_timeout: 10s\n") {
		t.Fatalf("ConfigYAML is not deterministic:\n%s", merged)
	}
}
