package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
)

// loopTestLogger discards everything; the loop only logs when recovering from
// a panic or when a restoration fails.
func loopTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// loopClock replaces time.After in the loop. Every wait is recorded, and the
// test drives it with advance. An advance that arrives before the wait is
// remembered and fires the next wait at once.
type loopClock struct {
	mu      sync.Mutex
	waits   []time.Duration
	pending chan time.Time
	queued  int
}

func newLoopClock() *loopClock {
	return &loopClock{}
}

func (c *loopClock) after(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	if c.queued > 0 {
		c.queued--
		ch <- time.Now()
	} else {
		c.pending = ch
	}
	c.mu.Unlock()
	return ch
}

func (c *loopClock) advance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != nil {
		c.pending <- time.Now()
		c.pending = nil
		return
	}
	c.queued++
}

func (c *loopClock) durations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.waits))
	copy(out, c.waits)
	return out
}

// loopWaitCount waits until the loop has requested n waits and returns them.
func loopWaitCount(t *testing.T, c *loopClock, n int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.durations(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d backoff waits; saw %v", n, c.durations())
	return nil
}

// loopLauncher is a launcher stub. fn runs for every launch.
type loopLauncher struct {
	mu    sync.Mutex
	calls int
	fn    func(context.Context) error
}

func (l *loopLauncher) Launch(ctx context.Context) error {
	l.mu.Lock()
	l.calls++
	fn := l.fn
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (l *loopLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// loopNoLaunch fails the test if a Broker is ever launched.
type loopNoLaunch struct{ t *testing.T }

func (l *loopNoLaunch) Launch(context.Context) error {
	l.t.Errorf("the connection loop launched a Broker when it must not")
	return errors.New("launch must not be called")
}

// loopWaitStatus polls the status store until cond holds.
func loopWaitStatus(t *testing.T, st *statusStore, what string, cond func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := st.Get()
		if cond(s) {
			return s
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last status %+v", what, st.Get())
	return Status{}
}

// loopNextRegister waits for the next Register stream on srv.
func loopNextRegister(t *testing.T, srv *fakelocal.Server) *fakelocal.RegisterConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := srv.NextRegister(ctx)
	if err != nil {
		t.Fatalf("waiting for a Register stream: %v", err)
	}
	return conn
}

// loopAwaitString waits for one value on ch.
func loopAwaitString(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

// loopCountCalls counts the unary calls recorded under method.
func loopCountCalls(srv *fakelocal.Server, method string) int {
	n := 0
	for _, c := range srv.Calls() {
		if c.Method == method {
			n++
		}
	}
	return n
}

// loopWaitCalls waits until calls() reaches want.
func loopWaitCalls(t *testing.T, calls func() int, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if calls() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to be called %d times; saw %d", what, want, calls())
}

// loopRetryPaused asserts that, with the injected timer held, the loop has
// made exactly one call to what and is blocked on the clock. The first request
// for a timer is the barrier: reaching it means the loop made its one call and
// then blocked, and a loop that retried without waiting would never get there.
// Waiting for the barrier instead of sleeping keeps the assertion free of a
// fixed settle window. It then releases the first wait and checks that the
// following call happens, so the pause is a wait rather than a dead end.
func loopRetryPaused(t *testing.T, calls func() int, what string, clock *loopClock) {
	t.Helper()
	waits := loopWaitCount(t, clock, 1)
	if got := calls(); got != 1 {
		t.Fatalf("%s calls when the loop paused on the timer = %d, want 1", what, got)
	}
	if waits[0] != 250*time.Millisecond {
		t.Fatalf("first wait = %v, want 250ms", waits[0])
	}
	clock.advance()
	loopWaitCalls(t, calls, 2, what)
}

// loopBaseDeps builds dependencies that use the real socket probe, dial,
// handshake and registration implementation; only the selection, launcher,
// clock and onComplete are stubbed per test.
func loopBaseDeps(st *statusStore, clock *loopClock) loopDeps {
	return loopDeps{
		probe:        probeSocket,
		dial:         dialLocal,
		hello:        hello,
		waitNotReady: waitNotReady,
		version:      localpb.ProtocolVersion,
		register:     runRegistration,
		first:        &localpb.RegisterRequest{InboxSocketPath: "/s", ClaudeConfigDir: "/c"},
		st:           st,
		onComplete:   func(context.Context, localpb.LocalClient, string, bool) {},
		after:        clock.after,
		log:          loopTestLogger(),
	}
}

// loopStart runs runLoop in the background. It returns a stop function that
// cancels the loop and waits for it, also registered as a cleanup.
func loopStart(t *testing.T, deps loopDeps) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, deps)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Errorf("runLoop did not return within 3s of cancelling its context")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestRunLoop(t *testing.T) {
	t.Run("user-level reconnects to the same Broker and registers again", func(t *testing.T) {
		// REQ-broker-config-037, REQ-topics-022
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})
		newServers := make(chan *fakelocal.Server, 1)
		completed := make(chan string, 8)

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		launcher := &loopLauncher{fn: func(ctx context.Context) error {
			s := fakelocal.Start(t, path, fakelocal.Handlers{})
			select {
			case newServers <- s:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		deps.launch = launcher
		deps.onComplete = func(_ context.Context, _ localpb.LocalClient, agentID string, _ bool) {
			completed <- agentID
		}
		stop := loopStart(t, deps)

		first := loopNextRegister(t, srv)
		if got := first.First.GetInboxSocketPath(); got != "/s" {
			t.Fatalf("first frame inbox_socket_path = %q, want %q", got, "/s")
		}
		if err := first.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		if got := loopAwaitString(t, completed, "the first completion"); got != "id1" {
			t.Fatalf("onComplete agent ID = %q, want id1", got)
		}

		srv.Stop()

		var replacement *fakelocal.Server
		select {
		case replacement = <-newServers:
		case <-time.After(3 * time.Second):
			t.Fatal("the loop did not launch a replacement Broker")
		}
		if launcher.count() != 1 {
			t.Fatalf("launch calls = %d, want 1", launcher.count())
		}
		second := loopNextRegister(t, replacement)
		if got, want := second.First.GetInboxSocketPath(), first.First.GetInboxSocketPath(); got != want {
			t.Errorf("second first frame inbox_socket_path = %q, want %q", got, want)
		}
		if got, want := second.First.GetClaudeConfigDir(), first.First.GetClaudeConfigDir(); got != want {
			t.Errorf("second first frame claude_config_dir = %q, want %q", got, want)
		}
		if err := second.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending the second registration_complete: %v", err)
		}
		if got := loopAwaitString(t, completed, "the second completion"); got != "id1" {
			t.Fatalf("second onComplete agent ID = %q, want id1", got)
		}
		// Cancel while the replacement Broker still serves the second Register
		// stream. Letting the Broker's cleanup stop the stream first makes the
		// loop launch another Broker during cleanup, whose own cleanup then runs
		// before this loop's cancel.
		stop()
	})

	t.Run("user-level reconnect never selects a different Broker", func(t *testing.T) {
		// REQ-broker-config-037
		userPath := fakelocal.SocketPath(t)
		systemPath := fakelocal.SocketPath(t)
		userSrv := fakelocal.Start(t, userPath, fakelocal.Handlers{})
		systemSrv := fakelocal.Start(t, systemPath, fakelocal.Handlers{})
		newServers := make(chan *fakelocal.Server, 1)

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: userPath, Kind: kindUser}, nil
		}
		var launches atomic.Int32
		deps.launch = &loopLauncher{fn: func(ctx context.Context) error {
			if launches.Add(1) == 1 {
				return &launchError{Reason: "the first launch failed"}
			}
			s := fakelocal.Start(t, userPath, fakelocal.Handlers{})
			select {
			case newServers <- s:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		stop := loopStart(t, deps)

		first := loopNextRegister(t, userSrv)
		if err := first.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		userSrv.Stop()

		waits := loopWaitCount(t, clock, 1)
		if waits[0] != time.Second {
			t.Fatalf("first wait = %v, want 1s after a failed launch", waits[0])
		}
		clock.advance()

		var replacement *fakelocal.Server
		select {
		case replacement = <-newServers:
		case <-time.After(3 * time.Second):
			t.Fatal("the loop did not launch the user-level Broker on the second attempt")
		}
		second := loopNextRegister(t, replacement)
		if got, want := second.First.GetInboxSocketPath(), first.First.GetInboxSocketPath(); got != want {
			t.Errorf("second first frame inbox_socket_path = %q, want %q", got, want)
		}

		// The second registration is the later event that proves the loop never
		// fell through to the system-level Broker; its Call log stays empty.
		if calls := systemSrv.Calls(); len(calls) != 0 {
			t.Errorf("the system-level Broker received %d calls, want none: %v", len(calls), calls)
		}
		// Cancel while the replacement still serves the second Register stream.
		stop()
	})

	t.Run("system-level Broker is retried, never launched or replaced", func(t *testing.T) {
		// design:108
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindSystem}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		stop := loopStart(t, deps)

		first := loopNextRegister(t, srv)
		if err := first.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		srv.Stop()

		waits := loopWaitCount(t, clock, 1)
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("first wait = %v, want 250ms", waits[0])
		}
		replacement := fakelocal.Start(t, path, fakelocal.Handlers{})
		clock.advance()

		second := loopNextRegister(t, replacement)
		if got, want := second.First.GetInboxSocketPath(), first.First.GetInboxSocketPath(); got != want {
			t.Errorf("first frame after the restart = %q, want %q", got, want)
		}
		// Cancel while the replacement still serves the second Register stream.
		stop()
	})

	t.Run("AGENT_PEER_SOCKET Broker unavailable then up", func(t *testing.T) {
		// REQ-agent-messaging-038
		path := fakelocal.SocketPath(t)
		var selectCalls atomic.Int32

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			if selectCalls.Add(1) == 1 {
				return target{}, fmt.Errorf("the socket %s named by AGENT_PEER_SOCKET is unavailable (missing)", path)
			}
			return target{Path: path, Kind: kindEnv}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		stop := loopStart(t, deps)

		status := loopWaitStatus(t, st, "PhaseUnavailable", func(s Status) bool {
			return s.Phase == PhaseUnavailable
		})
		if !strings.Contains(status.Detail, "AGENT_PEER_SOCKET") {
			t.Fatalf("Detail = %q, want it to name AGENT_PEER_SOCKET", status.Detail)
		}
		waits := loopWaitCount(t, clock, 1)
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("first wait = %v, want 250ms", waits[0])
		}
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})
		clock.advance()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		loopWaitStatus(t, st, "the registered agent", func(s Status) bool { return s.AgentID == "id1" })
		// Cancel while the Broker still serves the Register stream.
		stop()
	})

	t.Run("a launch failure backs off and retries the full selection", func(t *testing.T) {
		// REQ-broker-config-024, REQ-agent-messaging-038
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})
		var selectCalls atomic.Int32

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			if selectCalls.Add(1) < 3 {
				return target{}, &launchError{Reason: "listen address :17443 is unavailable"}
			}
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		status := loopWaitStatus(t, st, "PhaseLaunchFailed", func(s Status) bool {
			return s.Phase == PhaseLaunchFailed
		})
		if status.Detail != "listen address :17443 is unavailable" {
			t.Fatalf("Detail = %q, want the Broker's reason verbatim", status.Detail)
		}

		waits := loopWaitCount(t, clock, 1)
		if waits[0] != time.Second {
			t.Fatalf("first launch wait = %v, want 1s", waits[0])
		}
		clock.advance()
		waits = loopWaitCount(t, clock, 2)
		if waits[1] != 2*time.Second {
			t.Fatalf("second launch wait = %v, want 2s", waits[1])
		}
		clock.advance()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
	})

	t.Run("version mismatch waits for the Broker instead of polling", func(t *testing.T) {
		// REQ-broker-config-016
		path := fakelocal.SocketPath(t)
		var brokerVersion atomic.Uint32
		brokerVersion.Store(2)
		handlers := fakelocal.Handlers{
			Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
				return &localpb.HelloResponse{ProtocolVersion: brokerVersion.Load()}, nil
			},
		}
		srv := fakelocal.Start(t, path, handlers)
		launcher := &loopLauncher{}

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = launcher
		// A restart leaves a window in which the socket file is gone. Gate the
		// probe on the replacement listener so the loop sees a Broker that
		// answers and takes the reconnect backoff instead of the launch path.
		restarted := make(chan struct{})
		var restarting atomic.Bool
		probe := deps.probe
		deps.probe = func(ctx context.Context, p string) probeResult {
			if restarting.Load() {
				select {
				case <-restarted:
				case <-ctx.Done():
				}
			}
			return probe(ctx, p)
		}
		loopStart(t, deps)

		status := loopWaitStatus(t, st, "PhaseVersionMismatch", func(s Status) bool {
			return s.Phase == PhaseVersionMismatch
		})
		if !strings.Contains(status.Remedy, "restart the Broker") {
			t.Fatalf("Remedy = %q, want it to say restart the Broker", status.Remedy)
		}
		if !strings.HasPrefix(status.Detail, "the running Broker speaks local protocol version 2") {
			t.Fatalf("Detail = %q, want both versions", status.Detail)
		}

		if waits := clock.durations(); len(waits) != 0 {
			t.Fatalf("waits during the version mismatch = %v, want none", waits)
		}
		// The version-mismatch status is the later event: the probe returned and
		// the loop has not opened a Register stream.
		if got := loopCountCalls(srv, "Hello"); got != 1 {
			t.Fatalf("Hello calls at the version mismatch = %d, want 1 (no polling)", got)
		}
		if got := loopCountCalls(srv, "Register"); got != 0 {
			t.Fatalf("Register streams at the version mismatch = %d, want 0", got)
		}

		brokerVersion.Store(1)
		restarting.Store(true)
		srv.Stop()
		srv = fakelocal.Start(t, path, handlers)
		close(restarted)

		// The Broker leaving READY is still a failed attempt: reconnect waits
		// one connection backoff before dialing again.
		waits := loopWaitCount(t, clock, 1)
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("reconnect wait after a version mismatch = %v, want 250ms", waits[0])
		}
		clock.advance()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		// The replacement listener's registration is the later event: the loop
		// probed it once and then opened exactly one Register stream.
		if got := loopCountCalls(srv, "Hello"); got != 1 {
			t.Fatalf("Hello calls on the replacement listener = %d, want 1", got)
		}
		if got := loopCountCalls(srv, "Register"); got != 1 {
			t.Fatalf("Register streams = %d after the second registration, want 1 (the post-mismatch attempt only)", got)
		}
		if launcher.count() != 0 {
			t.Errorf("launch calls = %d, want 0 while the socket still answers", launcher.count())
		}
	})

	t.Run("version mismatch of the system-level Broker tells the user to ask the administrator", func(t *testing.T) {
		// REQ-broker-config-016
		path := fakelocal.SocketPath(t)
		var brokerVersion atomic.Uint32
		brokerVersion.Store(2)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{
			Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
				return &localpb.HelloResponse{ProtocolVersion: brokerVersion.Load()}, nil
			},
		})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindSystem}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		status := loopWaitStatus(t, st, "PhaseVersionMismatch", func(s Status) bool {
			return s.Phase == PhaseVersionMismatch
		})
		if !strings.Contains(status.Remedy, "administrator") || !strings.Contains(status.Remedy, "restart the Broker") {
			t.Fatalf("Remedy = %q, want the administrator sentence", status.Remedy)
		}

		brokerVersion.Store(1)
		srv.Restart()

		waits := loopWaitCount(t, clock, 1)
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("wait before reconnecting to the system-level Broker = %v, want 250ms", waits[0])
		}
		clock.advance()
		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
	})

	t.Run("version mismatch of the AGENT_PEER_SOCKET Broker names the socket in the remedy", func(t *testing.T) {
		// REQ-broker-config-016
		path := fakelocal.SocketPath(t)
		fakelocal.Start(t, path, fakelocal.Handlers{
			Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
				return &localpb.HelloResponse{ProtocolVersion: 2}, nil
			},
		})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindEnv}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		status := loopWaitStatus(t, st, "PhaseVersionMismatch", func(s Status) bool {
			return s.Phase == PhaseVersionMismatch
		})
		if !strings.Contains(status.Remedy, path) {
			t.Fatalf("Remedy = %q, want it to name the socket %q", status.Remedy, path)
		}
		if !strings.Contains(status.Remedy, "restart the Broker") {
			t.Fatalf("Remedy = %q, want it to say restart the Broker", status.Remedy)
		}
	})

	t.Run("AGENT_PEER_SOCKET Broker is only retried after a disconnect", func(t *testing.T) {
		// rulings.md: AGENT_PEER_SOCKET never launches a Broker
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindEnv}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		first := loopNextRegister(t, srv)
		if err := first.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		loopWaitStatus(t, st, "the registered agent", func(s Status) bool { return s.AgentID == "id1" })
		srv.Stop()

		for i, want := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
			waits := loopWaitCount(t, clock, i+1)
			if waits[i] != want {
				t.Fatalf("wait %d = %v, want %v", i+1, waits[i], want)
			}
			if i < 2 {
				clock.advance()
			}
		}
		replacement := fakelocal.Start(t, path, fakelocal.Handlers{})
		clock.advance()

		second := loopNextRegister(t, replacement)
		if err := second.Complete("id2", "me@h", false); err != nil {
			t.Fatalf("sending the second registration_complete: %v", err)
		}
	})

	t.Run("a user-level socket served by another account backs off without launching", func(t *testing.T) {
		// design:102, REQ-broker-config-037
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})

		var foreign atomic.Bool
		foreign.Store(true)

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		probe := deps.probe
		deps.probe = func(ctx context.Context, p string) probeResult {
			if foreign.Load() {
				return probeWrongPeer
			}
			return probe(ctx, p)
		}
		dial := deps.dial
		deps.dial = func(p string) (*grpc.ClientConn, localpb.LocalClient, error) {
			if foreign.Load() {
				return nil, nil, fmt.Errorf("the socket %s is served by another account", p)
			}
			return dial(p)
		}
		launcher := &loopLauncher{fn: func(context.Context) error {
			return errors.New("a Broker must not be launched for a socket served by another account")
		}}
		deps.launch = launcher
		loopStart(t, deps)

		waits := loopWaitCount(t, clock, 1)
		if status := st.Get(); status.Phase != PhaseUnavailable {
			t.Fatalf("Phase = %v while backing off, want PhaseUnavailable", status.Phase)
		}
		if got := launcher.count(); got != 0 {
			t.Fatalf("launch calls while the socket was served by another account = %d, want 0", got)
		}
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("first wait = %v, want 250ms", waits[0])
		}

		foreign.Store(false)
		clock.advance()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		loopWaitStatus(t, st, "the registered agent", func(s Status) bool { return s.AgentID == "id1" })
	})

	t.Run("a Hello failure on a user-level Broker waits before retrying", func(t *testing.T) {
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{
			Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
				return nil, status.Error(codes.Unavailable, "the Broker is shutting down")
			},
		})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		loopRetryPaused(t, func() int { return loopCountCalls(srv, "Hello") }, "Hello", clock)
	})

	t.Run("a Register failure right after Hello waits before retrying", func(t *testing.T) {
		path := fakelocal.SocketPath(t)
		fakelocal.Start(t, path, fakelocal.Handlers{})
		var registers atomic.Int32

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		deps.register = func(context.Context, localpb.LocalClient, *localpb.RegisterRequest,
			*statusStore, completeFunc, *slog.Logger) error {
			registers.Add(1)
			return errors.New("the Register stream was rejected at once")
		}
		loopStart(t, deps)

		loopRetryPaused(t, func() int { return int(registers.Load()) }, "Register", clock)
	})

	t.Run("a version mismatch on a user-level Broker that disconnects waits before retrying", func(t *testing.T) {
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{
			Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
				return &localpb.HelloResponse{ProtocolVersion: 2}, nil
			},
		})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		// The Broker answers Hello and then drops the connection, which is what
		// waitNotReady observes in production.
		deps.waitNotReady = func(context.Context, *grpc.ClientConn) {}
		loopStart(t, deps)

		loopRetryPaused(t, func() int { return loopCountCalls(srv, "Hello") }, "Hello", clock)
	})

	t.Run("a panic in one iteration does not end the loop", func(t *testing.T) {
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})
		var selectCalls atomic.Int32

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			if selectCalls.Add(1) == 1 {
				panic("boom")
			}
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		waits := loopWaitCount(t, clock, 1)
		if waits[0] != 250*time.Millisecond {
			t.Fatalf("wait after the panic = %v, want 250ms", waits[0])
		}
		clock.advance()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
	})

	t.Run("cancelling the context closes the Register stream and returns", func(t *testing.T) {
		// REQ-agent-messaging-034
		path := fakelocal.SocketPath(t)
		srv := fakelocal.Start(t, path, fakelocal.Handlers{})

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindUser}, nil
		}
		deps.launch = &loopNoLaunch{t: t}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			runLoop(ctx, deps)
		}()

		conn := loopNextRegister(t, srv)
		if err := conn.Complete("id1", "me@h", false); err != nil {
			t.Fatalf("sending registration_complete: %v", err)
		}
		loopWaitStatus(t, st, "the registered agent", func(s Status) bool { return s.AgentID == "id1" })

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("runLoop did not return within 3s of cancelling its context")
		}
		select {
		case <-conn.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("the Register stream stayed open after the context was cancelled")
		}
	})

	t.Run("the connection backoff doubles up to 5s", func(t *testing.T) {
		path := fakelocal.SocketPath(t)

		st := &statusStore{}
		clock := newLoopClock()
		deps := loopBaseDeps(st, clock)
		deps.selectTarget = func(context.Context) (target, error) {
			return target{Path: path, Kind: kindSystem}, nil
		}
		deps.launch = &loopNoLaunch{t: t}
		loopStart(t, deps)

		want := []time.Duration{
			250 * time.Millisecond,
			500 * time.Millisecond,
			time.Second,
			2 * time.Second,
			4 * time.Second,
			5 * time.Second,
			5 * time.Second,
		}
		for i, w := range want {
			waits := loopWaitCount(t, clock, i+1)
			if waits[i] != w {
				t.Fatalf("wait %d = %v, want %v", i+1, waits[i], w)
			}
			if i < len(want)-1 {
				clock.advance()
			}
		}
	})
}
