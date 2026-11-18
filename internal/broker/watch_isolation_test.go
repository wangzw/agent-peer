package broker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// recordedWatch is one Watch call seen by recordingWatchRouter.
type recordedWatch struct {
	uid uint32
	dir string
}

// recordingWatchRouter hands out one recording backend per account. A uid in
// block makes that account's Watch block until the channel is closed, ignoring
// the context, so a test controls exactly when the watch creation finishes.
type recordingWatchRouter struct {
	mu      sync.Mutex
	watches []recordedWatch
	block   map[uint32]chan struct{}
}

func newRecordingWatchRouter() *recordingWatchRouter {
	return &recordingWatchRouter{block: make(map[uint32]chan struct{})}
}

func (r *recordingWatchRouter) Acquire(_ context.Context, uid uint32) (SessionBackend, func(), error) {
	return &recordingWatchBackend{router: r, uid: uid}, func() {}, nil
}

func (r *recordingWatchRouter) watchCount(uid uint32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, w := range r.watches {
		if w.uid == uid {
			n++
		}
	}
	return n
}

func (r *recordingWatchRouter) totalWatches() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.watches)
}

func (r *recordingWatchRouter) blockFor(uid uint32, ch chan struct{}) {
	r.mu.Lock()
	r.block[uid] = ch
	r.mu.Unlock()
}

type recordingWatchBackend struct {
	router *recordingWatchRouter
	uid    uint32
}

func (b *recordingWatchBackend) Lookup(context.Context, string, string) (inbox.Session, error) {
	return inbox.Session{}, inbox.ErrNoRecord
}

func (b *recordingWatchBackend) Reread(_ context.Context, s inbox.Session) (inbox.Session, error) {
	return s, nil
}

func (b *recordingWatchBackend) Deliver(context.Context, inbox.Session, string, string, string, time.Duration) error {
	return nil
}

func (b *recordingWatchBackend) Watch(_ context.Context, configDir string, _ func(pid int)) (func(), error) {
	b.router.mu.Lock()
	b.router.watches = append(b.router.watches, recordedWatch{uid: b.uid, dir: configDir})
	block := b.router.block[b.uid]
	b.router.mu.Unlock()
	if block != nil {
		<-block
	}
	return func() {}, nil
}

// sequentialPeerCreds identifies each new local connection with the next uid,
// starting after first. The tests start one registration per connection and
// wait for its side effect before opening the next, so the order is fixed.
func sequentialPeerCreds(first uint32) func(net.Conn) (Caller, error) {
	var mu sync.Mutex
	next := first
	return func(net.Conn) (Caller, error) {
		mu.Lock()
		defer mu.Unlock()
		next++
		return Caller{UID: next}, nil
	}
}

func TestOwnedBackendVerifiesConfigDirOwner(t *testing.T) {
	me := uint32(os.Geteuid())
	own := t.TempDir()
	if err := (ownedBackend{uid: me}).verifyConfigDir(context.Background(), own); err != nil {
		t.Errorf("verifyConfigDir(%s) for its owner: %v", own, err)
	}
	if me == 0 {
		t.Skip("the test account owns every directory")
	}
	if err := (ownedBackend{uid: me}).verifyConfigDir(context.Background(), "/"); err == nil {
		t.Error("verifyConfigDir accepted a root-owned directory for a non-root account")
	}
}

func TestWatchesArePerAccount(t *testing.T) {
	router := newRecordingWatchRouter()
	tb := newTestBroker(t, func(d *Deps) {
		d.Sessions = router
		d.AllowCaller = func(Caller) bool { return true }
		d.ReadPeerCred = sequentialPeerCreds(9000)
	})

	dir := shortTempDir(t)

	// Both accounts use the default configuration directory, "". They must not
	// share one watch registration.
	startRegistrationAt(t, tb.client, "", filepath.Join(dir, "a.sock"))
	waitForReg(t, "the first account's watch", func() bool {
		return router.watchCount(9001) == 1
	})

	second := tb.newClient(t)
	startRegistrationAt(t, second, "", filepath.Join(dir, "b.sock"))
	waitForReg(t, "the second account's watch", func() bool {
		return router.watchCount(9002) == 1
	})

	if got := router.totalWatches(); got != 2 {
		t.Fatalf("Watch called %d times, want once per account", got)
	}
}

func TestHungWatchDoesNotBlockAnotherAccount(t *testing.T) {
	router := newRecordingWatchRouter()
	release := make(chan struct{})
	// The first connection is account 9001 and its watch creation never
	// returns until the test releases it.
	router.blockFor(9001, release)
	defer close(release)

	tb := newTestBroker(t, func(d *Deps) {
		d.Sessions = router
		d.AllowCaller = func(Caller) bool { return true }
		d.ReadPeerCred = sequentialPeerCreds(9000)
	})

	dir := shortTempDir(t)
	startRegistrationAt(t, tb.client, "", filepath.Join(dir, "a.sock"))
	waitForReg(t, "the first account's watch to block", func() bool {
		return router.watchCount(9001) == 1
	})

	// The second account registers the same directory string. Its watch must be
	// created without waiting for the first account's stuck registration.
	second := tb.newClient(t)
	startRegistrationAt(t, second, "", filepath.Join(dir, "b.sock"))
	waitForReg(t, "the second account's watch", func() bool {
		return router.watchCount(9002) == 1
	})
}
