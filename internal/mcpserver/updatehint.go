package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// toolsCallMethod is the MCP method the hint observes. Every tool call
	// passes through it, including calls that end in a tool error.
	toolsCallMethod = "tools/call"
	// updateStatusTimeout bounds one UpdateStatus query. A Broker that does not
	// answer in time only costs the hint, never the tool result.
	updateStatusTimeout = 3 * time.Second
	// updateRecheckInterval is the minimum time between two UpdateStatus
	// queries from one MCP process. The RPC is local and cheap, but querying on
	// every tool call buys nothing; ten minutes still notices a release while a
	// session stays open.
	updateRecheckInterval = 10 * time.Minute
)

// updateStatus is the part of the Broker's UpdateStatus response the hint
// needs. AvailableVersion is empty when no newer release is known.
type updateStatus struct {
	CurrentVersion   string
	AvailableVersion string
	ReleaseURL       string
}

// updateStatusSource queries the Broker for the update state. The hint skips
// itself on any error.
type updateStatusSource func(ctx context.Context) (updateStatus, error)

// updateHint appends a release notice to a tool result. The Broker is queried
// at most once per updateRecheckInterval per process, and the first tool call
// always queries. A notice is appended only when the available version is
// non-empty and differs from the version this process last notified, so the
// same version is never announced twice even though later calls re-query.
type updateHint struct {
	source  updateStatusSource
	timeout time.Duration
	log     *slog.Logger
	// fallbackCurrent is the version of this MCP process, used when the Broker
	// reports no current version.
	fallbackCurrent string
	// now is the clock. A nil value means time.Now.
	now func() time.Time

	mu              sync.Mutex
	checking        bool
	queried         bool
	lastQuery       time.Time
	notifiedVersion string
}

// middleware returns the receiving middleware that appends the notice. It runs
// the wrapped handler first, so the notice never replaces or delays the tool's
// own result; a call that fails at the protocol level (err != nil) has no
// result to decorate and is left alone.
func (u *updateHint) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if method != toolsCallMethod || err != nil || res == nil {
			return res, err
		}
		call, ok := res.(*mcp.CallToolResult)
		if !ok {
			return res, err
		}
		if text, ok := u.take(ctx); ok {
			call.Content = append(call.Content, &mcp.TextContent{Text: text})
		}
		return res, err
	}
}

// take returns the notice to append, if any. A query runs when no query is in
// flight and either the process has never had an answer or the recheck
// interval has passed. Only an answer from the Broker counts as a query; any
// query error, including Unimplemented, leaves the process unchecked so the
// next tool call retries. An empty available version or a version already
// notified yields false.
func (u *updateHint) take(ctx context.Context) (string, bool) {
	now := u.now
	if now == nil {
		now = time.Now
	}

	u.mu.Lock()
	at := now()
	if u.checking || (u.queried && at.Sub(u.lastQuery) < updateRecheckInterval) {
		u.mu.Unlock()
		return "", false
	}
	u.checking = true
	u.mu.Unlock()

	st, err := u.query(ctx)

	u.mu.Lock()
	u.checking = false
	if err == nil {
		u.queried = true
		u.lastQuery = at
	}
	if err != nil {
		u.mu.Unlock()
		if u.log != nil {
			u.log.Debug("the update check was skipped", "error", err)
		}
		return "", false
	}
	if st.AvailableVersion == "" {
		u.mu.Unlock()
		return "", false
	}
	if st.AvailableVersion == u.notifiedVersion {
		u.mu.Unlock()
		return "", false
	}
	u.notifiedVersion = st.AvailableVersion
	current := st.CurrentVersion
	if current == "" {
		current = u.fallbackCurrent
	}
	u.mu.Unlock()
	return hintText(st.AvailableVersion, current), true
}

// query runs the source with the query timeout. A panic from the source is
// reported as an error: a fault in the update check must never end the MCP
// server or eat a tool result.
func (u *updateHint) query(ctx context.Context) (st updateStatus, err error) {
	timeout := u.timeout
	if timeout <= 0 {
		timeout = updateStatusTimeout
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the update check panicked: %v", r)
			if u.log != nil {
				u.log.Error("panic in the update check", "panic", r)
			}
		}
	}()
	return u.source(qctx)
}

// hintText is the sentence the design fixes word for word.
func hintText(available, current string) string {
	return fmt.Sprintf("peers %s is available (this Broker runs %s). "+
		"Ask the agent to call update_broker, or run `peers update` in a terminal.",
		available, current)
}
