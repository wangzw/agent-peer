package mcpserver

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// completeFunc is called after every registration_complete event, in a
// goroutine of its own, so that joining topics never delays reading the
// Register stream. Calls are serialized: a completion that arrives while the
// previous one is still running waits for it.
type completeFunc func(ctx context.Context, c localpb.LocalClient, agentID string, autoJoin bool)

// runRegistration opens Register on c, sends first, and processes events until
// the stream ends or ctx is done. It never returns nil while the stream is
// healthy. On return it clears AgentID, Address and NotRegistered in st.
func runRegistration(ctx context.Context, c localpb.LocalClient, first *localpb.RegisterRequest,
	st *statusStore, onComplete completeFunc, log *slog.Logger) error {

	streamCtx, cancel := context.WithCancel(ctx)
	// Deferred in this order so that cancel runs first, then wg.Wait, then the
	// state is cleared: every onComplete sees its ctx cancelled before we wait
	// for it, and no stale identity survives after we return.
	var wg sync.WaitGroup
	defer clearRegistration(st)
	defer func() {
		// Publish the lost connection before waiting for the in-flight
		// completion and before clearing the identity. A tool call while the
		// completion finishes must be told the connection is gone and
		// reconnecting, not that registration is still in progress.
		loopSetStatus(st, PhaseUnavailable, loopLostText, "", nil)
		wg.Wait()
	}()
	defer cancel()

	stream, err := c.Register(streamCtx)
	if err != nil {
		return err
	}
	if err := stream.Send(first); err != nil {
		_ = stream.CloseSend()
		return err
	}

	// Capacity one serializes the onComplete calls without spawning a
	// dedicated worker that would outlive the stream.
	serial := make(chan struct{}, 1)

	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				_ = stream.CloseSend()
				return ctxErr
			}
			return err
		}

		switch event := resp.GetEvent().(type) {
		case *localpb.RegisterResponse_RegistrationComplete:
			complete := event.RegistrationComplete
			agentID := complete.GetAgentId()
			st.Update(func(s *Status) {
				s.AgentID = agentID
				s.Address = complete.GetAgentAddress()
				s.NotRegistered = localpb.NotRegisteredReason_NOT_REGISTERED_REASON_UNSPECIFIED
				s.NotRegisteredDetail = ""
			})
			autoJoin := complete.GetAutoJoin()
			wg.Add(1)
			go func() {
				defer wg.Done()
				runCompletion(streamCtx, serial, func() {
					onComplete(streamCtx, c, agentID, autoJoin)
				})
			}()

		case *localpb.RegisterResponse_NotRegistered:
			reason := event.NotRegistered.GetReason()
			detail := event.NotRegistered.GetDetail()
			st.Update(func(s *Status) {
				s.AgentID = ""
				s.Address = ""
				s.NotRegistered = reason
				s.NotRegisteredDetail = detail
			})

		case *localpb.RegisterResponse_AgentChanged:
			changed := event.AgentChanged
			st.Update(func(s *Status) {
				if s.AgentID != "" && s.AgentID == changed.GetAgentId() {
					s.Address = changed.GetAgentAddress()
				}
			})

		default:
			// An event this MCP server does not know yet: ignore it and keep
			// reading so that a newer Broker cannot break the stream.
			if log != nil {
				log.Debug("ignoring an unknown Register event")
			}
		}
	}
}

// runCompletion runs f once the serial slot is free, unless the stream context
// is done first. The slot keeps two completions from running at once.
func runCompletion(streamCtx context.Context, serial chan struct{}, f func()) {
	select {
	case serial <- struct{}{}:
	case <-streamCtx.Done():
		return
	}
	defer func() { <-serial }()
	if streamCtx.Err() != nil {
		// The cancellation can arrive while the previous completion still holds
		// the slot; both select cases are then ready at once, and the send case
		// may win. Do not run the callback for a connection that is gone.
		return
	}
	f()
}

// clearRegistration removes everything the stream had established, so no tool
// call reads a stale agent identity after the stream is gone.
func clearRegistration(st *statusStore) {
	st.Update(func(s *Status) {
		s.AgentID = ""
		s.Address = ""
		s.NotRegistered = localpb.NotRegisteredReason_NOT_REGISTERED_REASON_UNSPECIFIED
		s.NotRegisteredDetail = ""
	})
}
