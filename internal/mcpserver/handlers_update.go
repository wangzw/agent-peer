package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// Texts of update_broker results and errors. The result sentences and the
// "Upgrade failed" opening phrase are the wording of design-mcpserver.
const (
	textUpdateRestarting = "Upgrading the Broker from %s to %s; the Broker restarts and the connection recovers automatically. " +
		"Reopen this session to use the new MCP server."
	textUpdateCurrent  = "The Broker is already up to date (%s)."
	textUpdateFailed   = "Upgrade failed"
	textUpdateTerminal = "Run `agent-peer update` in a terminal instead."
	textUpdateAborted  = "an update is already in progress"
)

// UpdateBroker asks the Broker to upgrade itself to the latest release and
// restart. It returns as soon as the Broker answers: the Broker sends the reply
// before it schedules the restart, so the result describes what is about to
// happen rather than what has finished. No agent identity is needed; the Broker
// judges the caller by the account on the other end of the local socket.
func (h *handlers) UpdateBroker(ctx context.Context, in NoArgs) (UpdateBrokerOut, error) {
	st, err := h.preflight("update_broker", callGeneric, false)
	if err != nil {
		return UpdateBrokerOut{}, err
	}

	resp, err := st.Client.ApplyUpdate(ctx, &localpb.ApplyUpdateRequest{})
	if err != nil {
		return UpdateBrokerOut{}, updateBrokerError(err)
	}

	out := UpdateBrokerOut{
		FromVersion: resp.GetFromVersion(),
		ToVersion:   resp.GetToVersion(),
		Restarting:  resp.GetRestarting(),
	}
	if out.Restarting {
		out.Result = fmt.Sprintf(textUpdateRestarting, out.FromVersion, out.ToVersion)
	} else {
		out.Result = fmt.Sprintf(textUpdateCurrent, out.FromVersion)
	}
	return out, nil
}

// updateBrokerError turns an ApplyUpdate failure into the text shown to the
// model. The Broker describes these outcomes with bare gRPC status codes rather
// than an ErrorDetail, so the status code selects the text; every other failure
// goes through the shared mapping.
func updateBrokerError(err error) error {
	s := status.Convert(err)
	message := stripResultPrefix(s.Message())
	switch s.Code() {
	case codes.PermissionDenied:
		if message == "" {
			message = "permission denied"
		}
		return errors.New(toolError(textUpdateFailed, message, localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED).Error() + ". " + textNeedWrite)
	case codes.FailedPrecondition:
		if message == "" {
			message = "the Broker cannot update itself in its current state"
		}
		return fmt.Errorf("%s: %s (code: failed_precondition). %s", textUpdateFailed, message, textUpdateTerminal)
	case codes.Aborted:
		if message == "" {
			message = textUpdateAborted
		}
		return fmt.Errorf("%s: %s (code: aborted)", textUpdateFailed, message)
	default:
		return rpcError("update_broker", callUpdate, permWrite, err)
	}
}
