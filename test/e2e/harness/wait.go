package harness

import (
	"testing"
	"time"
)

// pollInterval is how often Eventually and Consistently re-run their
// condition. The interval is an implementation detail; callers bound the
// total wait against the timers under test instead.
const pollInterval = 50 * time.Millisecond

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
