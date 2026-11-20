package broker

import (
	"context"
	"sync"
	"time"
)

// linkWaiter waits for the link to a peer to come up without polling. It counts
// up events as a per-peer generation and wakes every waiter of that peer on one
// event.
//
// A waiter never reads Established. While a link is dropping, its state handler
// has not run yet and Established still reports true, so a sender that trusted
// that reading would burn its retries inside a window where the transport
// already refuses frames.
type linkWaiter struct {
	mu    sync.Mutex
	gen   map[peerID]uint64
	ch    map[peerID]map[chan struct{}]struct{}
	link  deliveryLink
	clock Clock
}

// newLinkWaiter subscribes to link up/down events. It must be created before
// the link starts so that no up event is missed. A nil link yields a waiter
// that never reports the link as established; a nil clock uses the system
// clock.
func newLinkWaiter(l deliveryLink, clock Clock) *linkWaiter {
	if clock == nil {
		clock = SystemClock()
	}
	w := &linkWaiter{
		gen:   make(map[peerID]uint64),
		ch:    make(map[peerID]map[chan struct{}]struct{}),
		link:  l,
		clock: clock,
	}
	if l != nil {
		l.Subscribe(w.onUp, func(peerID) {})
	}
	return w
}

// onUp records one up event and wakes every waiter of p. A down event needs no
// action: waiters keep waiting for the next up event.
func (w *linkWaiter) onUp(p peerID, _ []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gen[p]++
	for ch := range w.ch[p] {
		close(ch)
	}
	delete(w.ch, p)
}

// generation returns how many up events have been observed for p. A caller that
// watched a send fail passes the value it saw to wait, so only an up event that
// follows the failure satisfies that wait.
func (w *linkWaiter) generation(p peerID) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.gen[p]
}

// wait returns true once an up event for p follows the wait, false when max
// elapses or ctx ends first.
//
// A caller that observed a transport failure while the link may still have read
// as established passes the generation it saw: only a later up satisfies the
// wait. Without a generation, the generation at the call is the starting point,
// and any up from then on wakes the waiter. The Established reading is never
// consulted, so a stale established state cannot make the wait return at once.
func (w *linkWaiter) wait(ctx context.Context, p peerID, max time.Duration, since ...uint64) bool {
	if w.link == nil {
		return false
	}

	// The registration, the generation check and the timer share one lock: an
	// up event is either counted before the check (and satisfies the wait) or
	// sees the channel and closes it, and a caller that observes the
	// registration also knows the timer exists. The clock is injected so a test
	// advances time itself instead of racing a wall-clock timer.
	w.mu.Lock()
	if len(since) > 0 && w.gen[p] > since[0] {
		w.mu.Unlock()
		return true
	}
	timerCh := make(chan struct{})
	timer := w.clock.AfterFunc(max, func() { close(timerCh) })
	ch := make(chan struct{})
	if w.ch[p] == nil {
		w.ch[p] = make(map[chan struct{}]struct{})
	}
	w.ch[p][ch] = struct{}{}
	w.mu.Unlock()

	defer timer.Stop()
	defer func() {
		w.mu.Lock()
		if set := w.ch[p]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(w.ch, p)
			}
		}
		w.mu.Unlock()
	}()

	select {
	case <-ch:
		return true
	case <-timerCh:
		return false
	case <-ctx.Done():
		return false
	}
}
