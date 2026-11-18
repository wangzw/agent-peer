package broker

import (
	"context"
	"path/filepath"
	"sync"
)

// registryEvent is one notification from an inbox.Watch callback. The callback
// only records it; a per-account goroutine does the work, so the callback never
// waits on Broker.mu and a watch stop cannot deadlock against it.
type registryEvent struct {
	// uid is the account that owns the watch. The same directory string can
	// mean different directories to different accounts: "" is each account's
	// own default, so an event is never applied across accounts.
	uid uint32
	dir string
	pid int
}

// registryQueue dispatches registry events to one serial goroutine per account.
// An account's goroutine is created when the account's first event arrives and
// exits when the account's queue is empty, so a helper that one account has
// stopped cannot delay another account's renames, exits or registration
// retries. Events of one account stay in arrival order.
//
// Events are merged: one (dir, pid) is kept once, and a pid 0 event, which
// covers every record of its directory, replaces the pid events of the same
// directory. With no drop limit, an account's queue is bounded by the number of
// directories and pids it has, not by how many notifications arrive.
type registryQueue struct {
	mu      sync.Mutex
	lanes   map[uint32]*eventLane
	handled uint64
	ctx     context.Context
	handle  func(uid uint32, dir string, pid int)
}

// eventLane holds one account's pending events and whether its goroutine runs.
type eventLane struct {
	items   []registryEvent
	running bool
}

func newRegistryQueue() registryQueue {
	return registryQueue{lanes: make(map[uint32]*eventLane)}
}

// start records the Broker's lifetime context and the handler a lane runs.
func (q *registryQueue) start(ctx context.Context, handle func(uid uint32, dir string, pid int)) {
	q.mu.Lock()
	q.ctx = ctx
	q.handle = handle
	q.mu.Unlock()
}

func (q *registryQueue) push(uid uint32, dir string, pid int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	lane := q.lanes[uid]
	if lane == nil {
		lane = &eventLane{}
		q.lanes[uid] = lane
	}
	lane.add(registryEvent{uid: uid, dir: dir, pid: pid})
	if !lane.running {
		lane.running = true
		go q.drain(uid, lane)
	}
}

// add merges ev into the lane. An event already covered by a queued event of
// the same directory is dropped: a queued event with the same pid is a
// duplicate, and a queued pid 0 event covers every pid of that directory.
func (l *eventLane) add(ev registryEvent) {
	for _, queued := range l.items {
		if queued.dir == ev.dir && (queued.pid == 0 || queued.pid == ev.pid) {
			return
		}
	}
	if ev.pid == 0 {
		kept := l.items[:0]
		for _, queued := range l.items {
			if queued.dir != ev.dir {
				kept = append(kept, queued)
			}
		}
		l.items = kept
	}
	l.items = append(l.items, ev)
}

// drain runs one account's events until its queue is empty or the Broker stops,
// then lets the lane exit so an idle account holds no goroutine.
func (q *registryQueue) drain(uid uint32, lane *eventLane) {
	for {
		q.mu.Lock()
		if q.ctx != nil {
			select {
			case <-q.ctx.Done():
				lane.items = nil
				lane.running = false
				delete(q.lanes, uid)
				q.mu.Unlock()
				return
			default:
			}
		}
		if len(lane.items) == 0 {
			lane.running = false
			delete(q.lanes, uid)
			q.mu.Unlock()
			return
		}
		event := lane.items[0]
		lane.items = lane.items[1:]
		q.mu.Unlock()
		q.handle(event.uid, event.dir, event.pid)
	}
}

// markHandled records that a lane has looked at one registry event. Tests use
// handledCount to wait until an event has reached the agent table.
func (q *registryQueue) markHandled() {
	q.mu.Lock()
	q.handled++
	q.mu.Unlock()
}

func (q *registryQueue) handledCount() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.handled
}

// laneActive reports whether the account's event goroutine is still running.
// Tests use it to wait for the lane to finish its queue before counting work.
func (q *registryQueue) laneActive(uid uint32) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	lane := q.lanes[uid]
	return lane != nil && lane.running
}

// handleRegistryEvent rereads the agents of uid whose records live in dir and
// retries every registration connection of uid waiting on it. An event with pid
// 0 cannot be attributed to one record, so it refreshes every agent of that
// account's directory.
func (b *Broker) handleRegistryEvent(uid uint32, dir string, pid int) {
	b.mu.Lock()
	var agents []*agent
	for _, a := range b.agents {
		if a.watching && a.caller.UID == uid && a.watchDir.dir == dir && (pid == 0 || a.session.PID == pid) {
			agents = append(agents, a)
		}
	}
	var conns []*regConn
	for c := range b.waiting {
		if c.watching && c.caller.UID == uid && c.watchDir.dir == dir && c.socketPath != "" {
			// The connection may be between reading its record and attaching
			// its agent. Marking the retry here, under Broker.mu, makes the
			// running attempt reread after it attaches instead of losing this
			// change.
			c.retry.Store(true)
			conns = append(conns, c)
		}
	}
	b.mu.Unlock()
	b.events.markHandled()

	for _, a := range agents {
		b.refreshAgent(a)
	}
	for _, c := range conns {
		b.attempt(c)
	}
}

// watchKey identifies one account's watch on a configuration directory. The
// uid is part of the key because the same string means different directories to
// different accounts: "" is each account's own default, and even an absolute
// path must not let one account share or block another account's watch. The
// empty string keeps its own slot; every other value is cleaned.
type watchKey struct {
	uid uint32
	dir string
}

func watchKeyFor(uid uint32, configDir string) watchKey {
	if configDir != "" {
		configDir = filepath.Clean(configDir)
	}
	return watchKey{uid: uid, dir: configDir}
}
