package pairing

import (
	"context"
	"time"
)

// clock is the injectable time source. Pairing never calls time.Now or
// time.NewTimer directly, so tests can advance time without sleeping.
type clock interface {
	Now() time.Time
	NewTimer(d time.Duration) timer
}

// timer is the part of *time.Timer pairing uses.
type timer interface {
	C() <-chan time.Time
	Stop() bool
}

// realClock is the production clock.
type realClock struct{}

// Now returns the current wall-clock time.
func (realClock) Now() time.Time { return time.Now() }

// NewTimer starts a real timer.
func (realClock) NewTimer(d time.Duration) timer { return realTimer{t: time.NewTimer(d)} }

// realTimer adapts *time.Timer to timer.
type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// withClockTimeout returns a child context that is canceled when parent is
// canceled or when the injected clock's timer expires d from now. The timer is
// stopped on every exit path so a cancelled handshake does not leave it running.
func withClockTimeout(parent context.Context, c clock, d time.Duration) (context.Context, context.CancelFunc) {
	if c == nil {
		c = realClock{}
	}
	ctx, cancel := context.WithCancel(parent)
	t := c.NewTimer(d)
	go func() {
		select {
		case <-parent.Done():
		case <-t.C():
			cancel()
		case <-ctx.Done():
		}
		t.Stop()
	}()
	return ctx, cancel
}
