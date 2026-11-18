package broker

import (
	"path/filepath"
	"sync"
)

// registryEvent is one notification from an inbox.Watch callback. The callback
// only records it; the Broker's single event goroutine does the work, so the
// callback never waits on Broker.mu and a watch stop cannot deadlock against
// it.
type registryEvent struct {
	dir string
	pid int
}

// registryQueue is a mutex-protected event slice with a capacity-one wake-up
// signal. push never blocks.
type registryQueue struct {
	mu      sync.Mutex
	items   []registryEvent
	signal  chan struct{}
	handled uint64
}

func newRegistryQueue() registryQueue {
	return registryQueue{signal: make(chan struct{}, 1)}
}

func (q *registryQueue) push(dir string, pid int) {
	q.mu.Lock()
	q.items = append(q.items, registryEvent{dir: dir, pid: pid})
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// markHandled records that the event loop has looked at one registry event.
// Tests use handledCount to wait until an event has reached the agent table.
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

func (q *registryQueue) pop() (registryEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return registryEvent{}, false
	}
	event := q.items[0]
	q.items = q.items[1:]
	return event, true
}

// eventLoop processes registry events until the Broker stops.
func (b *Broker) eventLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.events.signal:
			for {
				event, ok := b.events.pop()
				if !ok {
					break
				}
				b.handleRegistryEvent(event.dir, event.pid)
			}
		}
	}
}

// handleRegistryEvent rereads the agents whose records live in dir and retries
// every registration connection waiting on it. An event with pid 0 cannot be
// attributed to one record, so it refreshes every agent of the directory.
func (b *Broker) handleRegistryEvent(dir string, pid int) {
	b.mu.Lock()
	var agents []*agent
	for _, a := range b.agents {
		if a.watching && a.watchDir == dir && (pid == 0 || a.session.PID == pid) {
			agents = append(agents, a)
		}
	}
	var conns []*regConn
	for c := range b.waiting {
		if c.watching && c.watchDir == dir && c.socketPath != "" {
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

// watchKey normalizes a configuration directory into the Broker.watches key.
// The empty string keeps its own slot; every other value is cleaned.
func watchKey(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Clean(configDir)
}
