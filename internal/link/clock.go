package link

import "time"

// realClock is the Clock backed by the standard library.
type realClock struct{}

// RealClock returns a Clock backed by time.Now and time.AfterFunc.
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) (stop func() bool) {
	return time.AfterFunc(d, f).Stop
}
