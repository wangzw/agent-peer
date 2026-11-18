package broker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// orderedSessions records when a registration handler releases its session
// backend and closes released the first time it happens, so the shutdown test
// can wait for the handler instead of sleeping.
type orderedSessions struct {
	*fakeSessions
	order    *orderLog
	released chan struct{}
	once     sync.Once
}

func (s *orderedSessions) Acquire(ctx context.Context, uid uint32) (SessionBackend, func(), error) {
	base, release, err := s.fakeSessions.Acquire(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	return base, func() {
		s.order.add("release")
		s.once.Do(func() { close(s.released) })
		release()
	}, nil
}

// dialRunLocal opens a client connection to a Broker that Run serves.
func dialRunLocal(t *testing.T, sock string) localpb.LocalClient {
	t.Helper()
	cc, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return localpb.NewLocalClient(cc)
}

// TestRunShutdownWaitsForRegistrationHandlers checks that Run does not return
// while a Register handler is still using the Broker. grpc.Server.Stop does not
// wait for method handlers by default, so shutdown could close the configuration
// and release the account lock under a handler that is still unregistering its
// agent. The criterion is: when Run returns, no watch is left and every
// Acquire was released.
func TestRunShutdownWaitsForRegistrationHandlers(t *testing.T) {
	sessions := &fakeSessions{}
	order := &orderLog{}
	released := make(chan struct{})
	captured := make(chan *Broker, 1)
	rt := newRunTest(t, func(rt *runTest) {
		rt.opts.Deps.Sessions = &orderedSessions{fakeSessions: sessions, order: order, released: released}
		rt.opts.Components = []Component{&orderComponent{
			name:     "capture",
			order:    order,
			captured: captured,
		}}
	})
	rt.start()
	if got := rt.readyLine(); got != "ok\n" {
		t.Fatalf("readiness line = %q", got)
	}

	var b *Broker
	select {
	case b = <-captured:
	case <-time.After(3 * time.Second):
		t.Fatal("the component never received the Broker")
	}

	// A valid absolute path with no session record: the handler acquires its
	// session backend, takes a watch on the configuration directory and waits
	// for the record. It stays in the handler until the stream ends.
	client := dialRunLocal(t, rt.socketPath())
	startRegistrationAt(t, client, t.TempDir(), filepath.Join(t.TempDir(), "s.sock"))

	// Wait until the handler holds its watch, which is the state it must leave
	// cleanly on shutdown.
	deadline := time.Now().Add(3 * time.Second)
	for {
		b.mu.Lock()
		n := len(b.watches)
		b.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the registration handler did not take a watch; watches = %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Holding Broker.mu keeps the handler inside dropConn. Cancel and release
	// the mutex: the handler then finishes and releases its backend, which is
	// the event shutdown must have waited for. The component Stop runs only
	// after Stop returns, and Stop waits for every handler, so it must be logged
	// after the release.
	b.mu.Lock()
	rt.cancel()
	b.mu.Unlock()

	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("the registration handler did not release its backend")
	}

	if err := rt.waitErr(); err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}

	entries := order.list()
	releasedAt, stoppedAt := -1, -1
	for i, entry := range entries {
		switch entry {
		case "release":
			releasedAt = i
		case "capture.Stop":
			stoppedAt = i
		}
	}
	if releasedAt < 0 || stoppedAt < 0 || stoppedAt < releasedAt {
		t.Fatalf("shutdown order = %v, want the handler's release before capture.Stop", entries)
	}

	b.mu.Lock()
	watches := len(b.watches)
	b.mu.Unlock()
	if watches != 0 {
		t.Errorf("b.watches has %d entries after Run returned, want 0", watches)
	}
	if got, want := sessions.ReleaseCount(), len(sessions.AcquiredUIDs()); got != want {
		t.Errorf("session backend releases = %d, Acquire calls = %d", got, want)
	}
}
