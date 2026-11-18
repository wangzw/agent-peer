package broker

import "time"

// Timer is a scheduled callback returned by Clock.AfterFunc. Stop reports
// whether it prevented the callback from running.
type Timer interface{ Stop() bool }

// Clock injects the passage of time so tests can advance it instead of
// sleeping. Production code uses SystemClock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// SystemClock returns the Clock backed by the time package. Its AfterFunc
// schedules f to run in its own goroutine, and its Timer.Stop reports whether
// the callback was stopped before it ran.
func SystemClock() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
