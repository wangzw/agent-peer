package harness

import (
	"testing"
	"time"
)

// pollInterval is how often Eventually and Consistently re-run their
// condition. The interval is an implementation detail; callers bound the
// total wait against the timers under test instead.
const pollInterval = 50 * time.Millisecond

// RegistrationReportBound is how long a test waits for the Broker's
// not_registered event when a session name is unusable. The Broker reports a
// registration that has not completed after registrationWait, which is 10
// seconds (internal/broker/register.go; design-broker.md:78); the two extra
// seconds let the event reach the MCP server process before the test gives up.
// This is the one wait that exceeds the three-second bound, because the bound
// is set by the product's own timer.
const RegistrationReportBound = 12 * time.Second

// Eventually calls cond immediately and then every 50ms until cond reports
// success or bound elapses. The first success returns. If the bound expires
// the test fails with the last state cond reported, which the caller uses to
// explain what it observed while waiting.
func Eventually(t testing.TB, bound time.Duration, what string, cond func() (ok bool, state string)) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		ok, state := cond()
		if ok {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s: not within %s (last state: %s)", what, bound, state)
			return
		}
		time.Sleep(pollInterval)
	}
}

// WaitRegistered waits until the session's MCP server has registered it with
// its Broker. list_agents needs the Agent identity and fails with
// not_registered until registration completes, so its success is the
// observable registration point. get_config and list_brokers do not need the
// identity and can succeed while the Broker is still registering, so they
// cannot serve as this wait.
func WaitRegistered(t testing.TB, s *Session, bound time.Duration) {
	t.Helper()
	Eventually(t, bound, "the session is registered", func() (bool, string) {
		r := s.Call(t, "list_agents", nil)
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
}

// Consistently calls cond immediately and then every 50ms for the whole
// window. Any failure ends the test at once with the state cond reported. The
// window is a deliberate observation of duration, so tests that use it record
// why the window is long enough.
func Consistently(t testing.TB, window time.Duration, what string, cond func() (ok bool, state string)) {
	t.Helper()
	deadline := time.Now().Add(window)
	for {
		ok, state := cond()
		if !ok {
			t.Fatalf("%s: failed within %s (state: %s)", what, window, state)
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(pollInterval)
	}
}
