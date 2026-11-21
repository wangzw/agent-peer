package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// loopDeps wires the background connection loop. Production fills every field;
// tests replace them with stubs so that no real Broker process is involved.
type loopDeps struct {
	selectTarget func(ctx context.Context) (target, error)
	probe        func(ctx context.Context, path string) probeResult
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

	loopRemedyUser   = "restart the Broker of this account with the same peers version, starting it with `peers service install` or with `peers broker` in a terminal outside the session; this session reconnects by itself"
	loopRemedySystem = "ask the administrator to restart the Broker system service with the same peers version; this session reconnects by itself"
	loopRemedyTest   = "restart the Broker at %s with the same peers version; this session reconnects by itself"
)

// loopRemedy returns the "To fix it, ..." clause for a target.
func loopRemedy(t target) string {
	switch t.Kind {
	case kindSystem:
		return loopRemedySystem
	case kindTest:
		return fmt.Sprintf(loopRemedyTest, t.Path)
	default:
		return loopRemedyUser
	}
}

// ConnBackoffCap is the longest wait between reconnection attempts to a known
// socket. It is exported so end-to-end tests can derive their wait bounds from
// it.
const ConnBackoffCap = 5 * time.Second

// loopConnBackoff paces reconnection attempts to a known socket: the first
// retry after a failure waits 250ms, later ones double up to ConnBackoffCap.
// Only a registration completion resets it; a successful Hello must not, or a
// Broker that fails every Register stream would be retried without any wait. It
// is safe for concurrent use because the reset runs from the goroutine that
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
	case b.wait < ConnBackoffCap:
		b.wait *= 2
		if b.wait > ConnBackoffCap {
			b.wait = ConnBackoffCap
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

// runLoop runs until ctx is done. It selects a Broker, connects, performs the
// version handshake and keeps the Register stream open. A lost connection
// reconnects to the same Broker and never starts one. Every iteration is
// guarded against panics, because no Broker-side failure may end this process.
func runLoop(ctx context.Context, d loopDeps) {
	conn := &loopConnBackoff{}
	if d.onComplete != nil {
		complete := d.onComplete
		d.onComplete = func(ctx context.Context, c localpb.LocalClient, agentID string, autoJoin bool) {
			conn.reset()
			complete(ctx, c, agentID, autoJoin)
		}
	}
	for ctx.Err() == nil {
		if loopOnce(ctx, d, conn) {
			return
		}
	}
}

// loopOnce performs one full pass: selection, connection and registration, or
// the wait that precedes the next attempt. It returns true when ctx is done
// and the loop must stop.
func loopOnce(ctx context.Context, d loopDeps, conn *loopConnBackoff) (stop bool) {
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
		if errors.Is(err, errNoBroker) {
			loopSetStatus(d.st, PhaseNoBroker, "", "", nil)
		} else {
			loopSetStatus(d.st, PhaseUnavailable, err.Error(), "", nil)
		}
		return !loopWait(ctx, d.after, conn.next())
	}

	for {
		if ctx.Err() != nil {
			return true
		}

		cc, client, err := d.dial(t.Path)
		if err != nil {
			phase, detail := loopFailureStatus(ctx, d, t,
				fmt.Sprintf("the Broker at %s is unavailable: %v", t.Path, err))
			loopSetStatus(d.st, phase, detail, "", nil)
			if !loopReconnect(ctx, d, conn) {
				return true
			}
			continue
		}

		res, err := d.hello(ctx, client, d.version)
		if err != nil {
			_ = cc.Close()
			phase, detail := loopFailureStatus(ctx, d, t,
				fmt.Sprintf("the Broker at %s is unavailable: %v", t.Path, err))
			loopSetStatus(d.st, phase, detail, "", nil)
			if !loopReconnect(ctx, d, conn) {
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
			if !loopReconnect(ctx, d, conn) {
				return true
			}
			continue
		}

		d.st.Update(func(s *Status) {
			s.Phase = PhaseConnected
			s.Detail = ""
			s.Remedy = ""
			s.Client = client
			s.Conn = cc
		})

		_ = d.register(ctx, client, d.first, d.st, d.onComplete, d.log)
		_ = cc.Close()
		if ctx.Err() != nil {
			return true
		}
		phase, detail := loopFailureStatus(ctx, d, t, loopLostText)
		loopSetStatus(d.st, phase, detail, "", nil)
		// Reconnect to the same target through the same rules as a failed dial,
		// so a Register stream that ends at once does not spin.
		if !loopReconnect(ctx, d, conn) {
			return true
		}
	}
}

// loopReconnect waits before the next attempt at the fixed target. It never
// starts a Broker; a Broker that has exited is left for the service manager or
// the user to start again. It returns false when ctx is done.
func loopReconnect(ctx context.Context, d loopDeps, conn *loopConnBackoff) bool {
	return loopWait(ctx, d.after, conn.next())
}

// loopFailureStatus chooses the phase for a failed attempt at t. A user-level
// socket that is missing or refuses connections has no Broker listening, so it
// is reported as no Broker with the same text as a session that never found
// one; that includes a Broker killed without removing its socket. A socket
// that answers and then fails is reported as unavailable. A system-level
// Broker that was selected and now fails is reported as no system-level
// Broker, so the user is sent to the administrator rather than to a user-level
// Broker (REQ-broker-config-037, REQ-broker-config-038).
func loopFailureStatus(ctx context.Context, d loopDeps, t target, detail string) (Phase, string) {
	switch t.Kind {
	case kindUser:
		probe := d.probe
		if probe == nil {
			probe = probeSocket
		}
		switch probe(ctx, t.Path) {
		case probeMissing, probeRefused:
			return PhaseNoBroker, ""
		}
	case kindSystem:
		return PhaseNoBroker, textNoSystemBroker
	}
	return PhaseUnavailable, detail
}

// loopSetStatus replaces the connection status with one that has no usable
// client or connection.
func loopSetStatus(st *statusStore, phase Phase, detail, remedy string, client localpb.LocalClient) {
	st.Update(func(s *Status) {
		s.Phase = phase
		s.Detail = detail
		s.Remedy = remedy
		s.Client = client
		s.Conn = nil
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
