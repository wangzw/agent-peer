package directory

import "time"

// clock supplies the current time and timers. Tests replace it to control the
// current instant without waiting.
type clock interface {
	Now() time.Time
	NewTimer(d time.Duration) timer
}

// timer is the subset of time.Timer the directory uses.
type timer interface {
	C() <-chan time.Time
	Stop() bool
}

// realClock is the production clock. A non-nil now decides "the current
// instant"; timers always use real time.
type realClock struct {
	now func() time.Time
}

// Now returns now() when set, and time.Now otherwise.
func (c realClock) Now() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// NewTimer returns a real-time timer.
func (c realClock) NewTimer(d time.Duration) timer {
	return &realTimer{t: time.NewTimer(d)}
}

// realTimer adapts *time.Timer to the timer interface.
type realTimer struct {
	t *time.Timer
}

func (t *realTimer) C() <-chan time.Time { return t.t.C }

func (t *realTimer) Stop() bool { return t.t.Stop() }
