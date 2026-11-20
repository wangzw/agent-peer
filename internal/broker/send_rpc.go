package broker

import (
	"context"
	"errors"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// Send serves the Local Send RPC. The caller must be a registered Agent of its
// own account; the send itself runs synchronously and its result is returned
// unchanged as a gRPC status with exactly one ErrorDetail on failure.
func (b *Broker) Send(ctx context.Context, req *localpb.SendRequest) (*localpb.SendResponse, error) {
	if _, err := b.agentFor(ctx, req.GetAgentId()); err != nil {
		return nil, toStatus(err)
	}

	snap, ok := b.lookupAgent(AgentID(req.GetAgentId()))
	if !ok {
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_NOT_REGISTERED,
			"agent %q is not registered for this account", req.GetAgentId()))
	}
	if b.deps.Delivery == nil {
		return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL,
			"message delivery is not assembled"))
	}

	if err := b.deps.Delivery.Send(ctx, viewOf(snap), req.GetToAddress(), req.GetMessage()); err != nil {
		return nil, toStatus(b.decoratePeerUnreachable(err, req.GetToAddress()))
	}
	return &localpb.SendResponse{}, nil
}

// decoratePeerUnreachable appends the local-only reason to a peer_unreachable
// error, but only when this Broker is the acceptor for that peer. A local-only
// Broker keeps no listener, so an acceptor-side peer cannot reach it; a peer
// this side dials is unaffected by the missing listener, and its failure has
// another cause.
func (b *Broker) decoratePeerUnreachable(err error, toAddress string) error {
	var e *Error
	if !errors.As(err, &e) || e.Code != localpb.ErrorCode_ERROR_CODE_PEER_UNREACHABLE {
		return err
	}
	degraded := b.degradedState()
	if degraded == nil ||
		degraded.Level != localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY ||
		degraded.Reason == "" {
		return err
	}
	if !b.sendPeerIsAcceptor(toAddress) {
		return err
	}
	e.Message += "; " + degraded.Reason
	return err
}

// sendPeerIsAcceptor reports whether the target address names a peer this
// Broker accepted the pairing for. A caller address without an alias, an
// unknown alias, and a dialer peer all return false.
func (b *Broker) sendPeerIsAcceptor(toAddress string) bool {
	addr, ok := parseTarget(toAddress)
	if !ok || addr.Alias == "" || b.deps.Delivery == nil {
		return false
	}
	peers := b.deps.Delivery.deps.Peers
	if peers == nil {
		return false
	}
	peer, ok := peers.ResolveAlias(addr.Alias)
	if !ok {
		return false
	}
	return peers.Role(peer) == pairing.RoleAcceptor
}
