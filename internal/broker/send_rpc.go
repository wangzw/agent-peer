package broker

import (
	"context"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
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
		return nil, toStatus(err)
	}
	return &localpb.SendResponse{}, nil
}
