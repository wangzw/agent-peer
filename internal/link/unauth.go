package link

import (
	"container/list"
	"net"
	"sync"
	"time"
)

// unauthRegistry tracks inbound connections until the peer behind them is
// authenticated. It enforces two limits from the design: a connection that has
// not sent its first frame within firstFrame is closed, and the number of
// unauthenticated connections never exceeds max; when it would, the oldest
// connection is closed.
type unauthRegistry struct {
	mu         sync.Mutex
	conns      list.List // of *trackedConn, oldest first
	max        int
	firstFrame time.Duration
	afterFunc  func(time.Duration, func()) func() bool
}

func newUnauthRegistry(max int, firstFrame time.Duration, afterFunc func(time.Duration, func()) func() bool) *unauthRegistry {
	return &unauthRegistry{
		max:        max,
		firstFrame: firstFrame,
		afterFunc:  afterFunc,
	}
}

// track registers c as unauthenticated and starts its first-frame deadline.
// The caller receives the tracked wrapper and should use it in place of c.
func (r *unauthRegistry) track(c net.Conn) *trackedConn {
	t := &trackedConn{Conn: c, reg: r}

	r.mu.Lock()
	t.elem = r.conns.PushBack(t)
	r.mu.Unlock()

	// The timer starts outside the registry lock: an injected afterFunc is
	// allowed to invoke its callback synchronously, and the callback needs the
	// lock. Production time.AfterFunc is asynchronous.
	stop := r.afterFunc(r.firstFrame, t.deadline)

	r.mu.Lock()
	if t.closed || t.seen {
		// The deadline fired before the timer was recorded, or the connection
		// was already gone. Keep no timer reference.
		r.mu.Unlock()
		if stop != nil {
			stop()
		}
	} else {
		t.timer = stop
		r.mu.Unlock()
	}

	r.evict()
	return t
}

func (r *unauthRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns.Len()
}

// evict closes the oldest connection while the registry is over its limit.
func (r *unauthRegistry) evict() {
	r.mu.Lock()
	var (
		stop func() bool
		raw  net.Conn
	)
	if r.conns.Len() > r.max {
		if front := r.conns.Front(); front != nil {
			t := front.Value.(*trackedConn)
			t.closed = true
			t.detachLocked()
			stop = t.stopTimerLocked()
			raw = t.Conn
		}
	}
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
	if raw != nil {
		_ = raw.Close()
	}
}

// trackedConn is an inbound connection under the first-frame deadline. All of
// its mutable state is guarded by reg.mu so that authentication and eviction
// cannot interleave: whichever reaches the lock first decides the outcome.
type trackedConn struct {
	net.Conn

	reg  *unauthRegistry
	elem *list.Element // nil once detached from the registry

	timer   func() bool // first-frame deadline; nil when none is pending
	seen    bool
	closed  bool
	claimed bool
}

// firstFrameSeen records that the peer sent its first frame. The connection
// stays counted as unauthenticated but is no longer closed by the deadline.
func (t *trackedConn) firstFrameSeen() {
	r := t.reg
	r.mu.Lock()
	if t.closed {
		r.mu.Unlock()
		return
	}
	t.seen = true
	stop := t.stopTimerLocked()
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
}

// authenticated removes the connection from the registry. It is no longer
// counted or eligible for eviction, and its deadline no longer applies.
func (t *trackedConn) authenticated() {
	r := t.reg
	r.mu.Lock()
	if t.closed {
		r.mu.Unlock()
		return
	}
	t.detachLocked()
	stop := t.stopTimerLocked()
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
}

// Close is idempotent. It removes the connection from the registry and closes
// the raw connection outside the registry lock.
func (t *trackedConn) Close() error {
	r := t.reg
	r.mu.Lock()
	if t.closed {
		r.mu.Unlock()
		return nil
	}
	t.closed = true
	t.detachLocked()
	stop := t.stopTimerLocked()
	r.mu.Unlock()

	if stop != nil {
		stop()
	}
	return t.Conn.Close()
}

// claimStream reports whether this is the first call. A tracked connection may
// carry only one gRPC stream, so later calls are refused before they can skip
// the first-frame check.
func (t *trackedConn) claimStream() bool {
	r := t.reg
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.claimed {
		return false
	}
	t.claimed = true
	return true
}

// deadline runs when the first-frame timer fires.
func (t *trackedConn) deadline() {
	r := t.reg
	r.mu.Lock()
	t.timer = nil // the timer has fired; there is nothing left to stop
	if t.closed || t.seen {
		r.mu.Unlock()
		return
	}
	t.closed = true
	t.detachLocked()
	r.mu.Unlock()

	_ = t.Conn.Close()
}

// detachLocked removes t from the registry. Caller holds reg.mu.
func (t *trackedConn) detachLocked() {
	if t.elem != nil {
		t.reg.conns.Remove(t.elem)
		t.elem = nil
	}
}

// stopTimerLocked clears the recorded timer and returns its stop function.
// Caller holds reg.mu and must call the returned function outside the lock.
func (t *trackedConn) stopTimerLocked() func() bool {
	stop := t.timer
	t.timer = nil
	return stop
}

// trackingListener wraps a listener so every accepted connection is tracked.
type trackingListener struct {
	net.Listener
	reg *unauthRegistry
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.reg.track(c), nil
}
