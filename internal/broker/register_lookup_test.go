package broker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/peers/internal/inbox"
	"github.com/wangzw/peers/internal/testkit/fakesession"
)

// blockingAfterLookupBackend blocks after the record of the first Lookup has
// been read, so a registry event can land between that read and the moment the
// agent enters the table.
type blockingAfterLookupBackend struct {
	SessionBackend
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingAfterLookupBackend) Lookup(ctx context.Context, configDir, socketPath string) (inbox.Session, error) {
	s, err := b.SessionBackend.Lookup(ctx, configDir, socketPath)
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
	return s, err
}

// lookupBlockingSessions hands out one blockingAfterLookupBackend wrapping the
// real backend.
type lookupBlockingSessions struct {
	SessionRouter
	backend *blockingAfterLookupBackend
}

func (s *lookupBlockingSessions) Acquire(ctx context.Context, uid uint32) (SessionBackend, func(), error) {
	base, release, err := s.SessionRouter.Acquire(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	s.backend.SessionBackend = base
	return s.backend, release, nil
}

// attemptGate blocks the attempt call that reaches attemptReadHook, which runs
// after the attempt has read retry and before it releases attemptMu.
type attemptGate struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *attemptGate) hook(*regConn) {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
}

func setAttemptReadHook(t *testing.T, fn func(c *regConn)) {
	t.Helper()
	attemptReadHook.Store(&fn)
	t.Cleanup(func() { attemptReadHook.Store(nil) })
}

func setAttemptBusyHook(t *testing.T, fn func(c *regConn)) {
	t.Helper()
	attemptBusyHook.Store(&fn)
	t.Cleanup(func() { attemptBusyHook.Store(nil) })
}

// withholdRecordBackend makes the first Lookup report a missing record even
// though the backing backend already holds one. That keeps the registration
// waiting without the test having to create a record after the watch exists, so
// the only registry event the test delivers is the one it pushes itself.
type withholdRecordBackend struct {
	SessionBackend
	once sync.Once
}

func (b *withholdRecordBackend) Lookup(ctx context.Context, configDir, socketPath string) (inbox.Session, error) {
	withheld := false
	b.once.Do(func() { withheld = true })
	if withheld {
		return inbox.Session{}, inbox.ErrNoRecord
	}
	return b.SessionBackend.Lookup(ctx, configDir, socketPath)
}

// withholdRecordSessions hands out one withholdRecordBackend wrapping the real
// backend.
type withholdRecordSessions struct {
	SessionRouter
	backend *withholdRecordBackend
}

func (s *withholdRecordSessions) Acquire(ctx context.Context, uid uint32) (SessionBackend, func(), error) {
	base, release, err := s.SessionRouter.Acquire(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	s.backend.SessionBackend = base
	return s.backend, release, nil
}

// TestRegisterKeepsRetryRequestedAtAttemptEnd covers the window between the
// running attempt reading retry and releasing attemptMu: a registry event that
// arrives in that window must still cause another pass, even though its own
// attempt call finds the lock held. Dropping it leaves the registration waiting
// for a later event and reporting not_registered after the whole wait.
func TestRegisterKeepsRetryRequestedAtAttemptEnd(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "s.sock")
	// The record exists before the watch does, so its write produces no registry
	// event; the first Lookup withholds it until the test has the connection
	// waiting.
	fake := fakesession.New(t, fakesession.Options{ConfigDir: dir, SocketPath: socketPath, Name: "late"})
	withheld := &withholdRecordBackend{}
	tb := newTestBroker(t, func(d *Deps) {
		d.Sessions = &withholdRecordSessions{SessionRouter: d.Sessions, backend: withheld}
	})

	gate := &attemptGate{started: make(chan struct{}), release: make(chan struct{})}
	setAttemptReadHook(t, gate.hook)
	busy := make(chan struct{}, 1)
	setAttemptBusyHook(t, func(*regConn) {
		select {
		case busy <- struct{}{}:
		default:
		}
	})

	rs := startRegistrationAt(t, tb.client, dir, socketPath)

	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the first attempt did not reach the pause")
	}

	// One registry event while the first attempt holds the lock. The event
	// goroutine's attempt finds the lock held and hands this change to the
	// running attempt; the test releases that attempt only after the hand-off.
	uid := uint32(tb.b.deps.Geteuid())
	tb.b.events.push(uid, watchKeyFor(uid, dir).dir, fake.PID())
	select {
	case <-busy:
	case <-time.After(3 * time.Second):
		t.Fatal("the registry event did not reach the running attempt")
	}

	close(gate.release)

	complete := rs.complete(3 * time.Second)
	if complete.GetAgentAddress() != "late@hostA" {
		t.Fatalf("agent_address = %q, want late@hostA", complete.GetAgentAddress())
	}
}

// TestRegisterKeepsChangeMadeDuringLookup checks that a record change handled
// while the first Lookup is in flight is not lost: the agent must end up with
// the record's new state instead of the snapshot taken before the change.
func TestRegisterKeepsChangeMadeDuringLookup(t *testing.T) {
	blocked := &blockingAfterLookupBackend{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	tb := newTestBroker(t, func(d *Deps) {
		d.Sessions = &lookupBlockingSessions{SessionRouter: d.Sessions, backend: blocked}
	})
	fake := fakesession.New(t, fakesession.Options{Name: "changing", Status: "idle"})
	rs := startRegistration(t, tb, fake)

	select {
	case <-blocked.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the first lookup did not complete within the deadline")
	}

	fake.SetStatus("busy")
	waitForReg(t, "the registry event to be handled", func() bool {
		return tb.b.events.handledCount() > 0
	})
	close(blocked.release)

	rs.complete(3 * time.Second)
	waitForReg(t, "the agent to pick up the status change", func() bool {
		snap, ok := tb.b.lookupAgent(mustAgentID(t, fake.SocketPath()))
		return ok && snap.Session.Status == "busy"
	})
}
