package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// loopDeps wires the background connection loop. Production fills every field;
// tests replace them with stubs so that no real Broker process is involved.
type loopDeps struct {
	selectTarget func(ctx context.Context) (target, error)
	probe        func(ctx context.Context, path string) probeResult
	launch       launcher
	dial         func(path string) (*grpc.ClientConn, localpb.LocalClient, error)
	hello        func(ctx context.Context, c localpb.LocalClient, version uint32) (helloResult, error)
	waitNotReady func(ctx context.Context, cc *grpc.ClientConn)
	version      uint32 // Options.LocalProtocolVersion, 0 already replaced by localpb.ProtocolVersion
	register     func(ctx context.Context, c localpb.LocalClient, first *localpb.RegisterRequest,
		st *statusStore, onComplete completeFunc, log *slog.Logger) error
	first      *localpb.RegisterRequest
	st         *statusStore
	onComplete completeFunc
	after      func(time.Duration) <-chan time.Time
	log        *slog.Logger
}

// Texts the loop puts into Status. The remedy clauses match the sentences the
// design requires for each kind of Broker and always contain "restart the
// Broker".
const (
	loopLostText = "the connection to the Broker was lost; reconnecting"

	loopRemedyUser   = "restart the Broker of this account with the same agent-peer version; this session reconnects by itself"
	loopRemedySystem = "ask the administrator to restart the Broker system service with the same agent-peer version; this session reconnects by itself"
	loopRemedyEnv    = "restart the Broker at %s with the same agent-peer version; this session reconnects by itself"
)

// loopRemedy returns the "To fix it, ..." clause for a target.
func loopRemedy(t target) string {
	switch t.Kind {
	case kindSystem:
		return loopRemedySystem
	case kindEnv:
		return fmt.Sprintf(loopRemedyEnv, t.Path)
	default:
		return loopRemedyUser
	}
}

// loopConnBackoff paces reconnection attempts to a known socket: the first
// retry after a failure waits 250ms, later ones double up to 5s. Only a
// registration completion resets it; a successful Hello must not, or a Broker
// that fails every Register stream would be retried without any wait. It is
// safe for concurrent use because the reset runs from the goroutine that
// handles registration_complete.
type loopConnBackoff struct {
	mu   sync.Mutex
	wait time.Duration
}

// next returns the wait before the next attempt and advances the sequence.
func (b *loopConnBackoff) next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.wait == 0:
		b.wait = 250 * time.Millisecond
	case b.wait < 5*time.Second:
		b.wait *= 2
		if b.wait > 5*time.Second {
			b.wait = 5 * time.Second
		}
	}
	return b.wait
}

// reset restarts the sequence at 250ms.
func (b *loopConnBackoff) reset() {
	b.mu.Lock()
	b.wait = 0
	b.mu.Unlock()
}

// loopLaunchBackoff paces re-running the full selection after launching the
// user-level Broker failed: 1s, 2s, 4s, ... up to 30s. A successful selection
// resets it.
type loopLaunchBackoff struct {
	mu   sync.Mutex
	wait time.Duration
}

// next returns the wait before the next selection and advances the sequence.
func (b *loopLaunchBackoff) next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.wait == 0:
		b.wait = time.Second
	case b.wait < 30*time.Second:
		b.wait *= 2
		if b.wait > 30*time.Second {
			b.wait = 30 * time.Second
		}
	}
	return b.wait
}

// reset restarts the sequence at 1s.
func (b *loopLaunchBackoff) reset() {
	b.mu.Lock()
	b.wait = 0
	b.mu.Unlock()
}

// runLoop runs until ctx is done. It selects a Broker, connects, performs the
// version handshake and keeps the Register stream open. A lost connection
// reconnects to the same Broker: the user-level one is launched again when its
// socket is gone, the system-level one and the one named by AGENT_PEER_SOCKET
// are only retried. Every iteration is guarded against panics, because no
// Broker-side failure may end this process.
func runLoop(ctx context.Context, d loopDeps) {
	conn := &loopConnBackoff{}
	launch := &loopLaunchBackoff{}
	if d.onComplete != nil {
		complete := d.onComplete
		d.onComplete = func(ctx context.Context, c localpb.LocalClient, agentID string, autoJoin bool) {
			conn.reset()
			complete(ctx, c, agentID, autoJoin)
		}
	}
	for ctx.Err() == nil {
		if loopOnce(ctx, d, conn, launch) {
			return
		}
	}
}

// loopOnce performs one full pass: selection, connection and registration, or
// the wait that precedes the next attempt. It returns true when ctx is done
// and the loop must stop.
func loopOnce(ctx context.Context, d loopDeps, conn *loopConnBackoff, launch *loopLaunchBackoff) (stop bool) {
	defer func() {
		if r := recover(); r != nil {
			if d.log != nil {
				d.log.Error("mcpserver: recovered from a panic in the connection loop", "panic", r)
			}
			stop = !loopWait(ctx, d.after, conn.next())
		}
	}()

	t, err := d.selectTarget(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return true
		}
		var le *launchError
		if errors.As(err, &le) {
			loopSetStatus(d.st, PhaseLaunchFailed, le.Reason, "", nil)
			return !loopWait(ctx, d.after, launch.next())
		}
		loopSetStatus(d.st, PhaseUnavailable, err.Error(), "", nil)
		return !loopWait(ctx, d.after, conn.next())
	}
	launch.reset()

	// launched records that a launch succeeded and its one immediate
	// connection attempt has not failed yet. The next launch waits the
	// connection backoff; the connection itself does not. A completed
	// registration ends the launch cycle and clears it. The completion runs
	// in its own goroutine, so the flag is atomic.
	var launched atomic.Bool
	complete := d.onComplete
	onComplete := func(ctx context.Context, c localpb.LocalClient, agentID string, autoJoin bool) {
		launched.Store(false)
		if complete != nil {
			complete(ctx, c, agentID, autoJoin)
		}
	}
	for {
		if ctx.Err() != nil {
			return true
		}

		cc, client, err := d.dial(t.Path)
		if err != nil {
			loopSetStatus(d.st, PhaseUnavailable,
				fmt.Sprintf("the Broker at %s is unavailable: %v", t.Path, err), "", nil)
			if !loopReconnect(ctx, d, t, conn, launch, &launched) {
				return true
			}
			continue
		}

		res, err := d.hello(ctx, client, d.version)
		if err != nil {
			_ = cc.Close()
			loopSetStatus(d.st, PhaseUnavailable,
				fmt.Sprintf("the Broker at %s is unavailable: %v", t.Path, err), "", nil)
			if !loopReconnect(ctx, d, t, conn, launch, &launched) {
				return true
			}
			continue
		}

		if !res.Compatible {
			// hello reports BrokerVersion 0 when the Broker refused the
			// handshake with a version_mismatch error, whose detail carries no
			// version field. Naming version 0 would claim something the Broker
			// never said, so that path describes the mismatch without it.
			detail := fmt.Sprintf("the running Broker speaks a different local protocol version, this MCP server speaks %d",
				d.version)
			if res.BrokerVersion != 0 {
				detail = fmt.Sprintf("the running Broker speaks local protocol version %d, this MCP server speaks %d",
					res.BrokerVersion, d.version)
			}
			loopSetStatus(d.st, PhaseVersionMismatch, detail, loopRemedy(t), nil)
			// The Broker keeps this connection open on a version mismatch, so
			// waiting for it to leave READY is the only reliable sign that the
			// administrator or the user restarted it. No requests are sent
			// while waiting.
			d.waitNotReady(ctx, cc)
			_ = cc.Close()
			if ctx.Err() != nil {
				return true
			}
			if !loopReconnect(ctx, d, t, conn, launch, &launched) {
				return true
			}
			continue
		}

		d.st.Update(func(s *Status) {
			s.Phase = PhaseConnected
			s.Detail = ""
			s.Remedy = ""
			s.Client = client
		})

		_ = d.register(ctx, client, d.first, d.st, onComplete, d.log)
		_ = cc.Close()
		if ctx.Err() != nil {
			return true
		}
		loopSetStatus(d.st, PhaseUnavailable, loopLostText, "", nil)
		// Reconnect to the same target through the same rules as a failed dial,
		// so a Register stream that ends at once does not spin.
		if !loopReconnect(ctx, d, t, conn, launch, &launched) {
			return true
		}
	}
}

// loopReconnect prepares the next attempt at the fixed target t. A user-level
// Broker is launched when its socket is gone; a socket that still answers, one
// served by another account, a system-level Broker and the one named by
// AGENT_PEER_SOCKET are only retried. It returns false when ctx is done.
func loopReconnect(ctx context.Context, d loopDeps, t target, conn *loopConnBackoff, launch *loopLaunchBackoff,
	launched *atomic.Bool) bool {
	if t.Kind != kindUser {
		return loopWait(ctx, d.after, conn.next())
	}
	switch d.probe(ctx, t.Path) {
	case probeOK:
		// The socket still answers, but the last attempt failed anyway (a Hello
		// error, a Register stream that ended at once, or a version mismatch
		// followed by a disconnect). Wait before retrying, or the loop spins as
		// fast as the Broker answers.
		return loopWait(ctx, d.after, conn.next())
	case probeWrongPeer:
		// Another account now serves the socket. Launching a Broker cannot take
		// the path back, so, as in the selection phase, treat the socket as
		// unavailable and back off instead of launching.
		return loopWait(ctx, d.after, conn.next())
	}
	if ctx.Err() != nil {
		// The session is shutting down; do not start a Broker on its way out.
		return false
	}

	if launched.Load() {
		// The launch got its one immediate connection attempt, which failed.
		// Pace the next launch by the connection backoff; a successful launch
		// must not reset it. This is not a connection attempt, so it does not
		// delay the connection that follows a successful launch.
		launched.Store(false)
		if !loopWait(ctx, d.after, conn.next()) {
			return false
		}
	}

	err := d.launch.Launch(ctx)
	if err == nil {
		launched.Store(true)
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	// Another MCP server process may have launched the Broker while this one
	// was waiting for its own launch; its Broker exits at once for lack of the
	// account lock, but the socket may now be served.
	if d.probe(ctx, t.Path) == probeOK {
		return true
	}

	reason := err.Error()
	var le *launchError
	if errors.As(err, &le) {
		reason = le.Reason
	}
	loopSetStatus(d.st, PhaseLaunchFailed, reason, "", nil)
	return loopWait(ctx, d.after, launch.next())
}

// loopSetStatus replaces the connection status with one that has no usable
// client.
func loopSetStatus(st *statusStore, phase Phase, detail, remedy string, client localpb.LocalClient) {
	st.Update(func(s *Status) {
		s.Phase = phase
		s.Detail = detail
		s.Remedy = remedy
		s.Client = client
	})
}

// loopWait waits d on the injected timer and reports whether the wait
// completed. It returns false when ctx is done first.
func loopWait(ctx context.Context, after func(time.Duration) <-chan time.Time, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if after == nil {
		after = time.After
	}
	select {
	case <-ctx.Done():
		return false
	case <-after(d):
		return true
	}
}
