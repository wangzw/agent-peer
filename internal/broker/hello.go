package broker

import (
	"context"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// Hello performs the Local protocol version handshake. The request's version
// must equal the Broker's local protocol version, either localpb.ProtocolVersion
// or the value injected through Deps.LocalProtocolVersion. On a mismatch it
// returns version_mismatch with a message that tells the user to restart the
// Broker, logs the mismatch and keeps the connection open. It marks the
// connection rejected and records both versions, so the version interceptors
// refuse every later call except Hello on that connection.
func (b *Broker) Hello(ctx context.Context, req *localpb.HelloRequest) (*localpb.HelloResponse, error) {
	v := b.deps.LocalProtocolVersion
	got := req.GetProtocolVersion()
	if got == v {
		return &localpb.HelloResponse{ProtocolVersion: v}, nil
	}

	caller, _ := CallerFrom(ctx)
	b.log.Warn(EvHelloVersionMismatch,
		"client_version", got,
		"broker_version", v,
		"uid", caller.UID,
	)
	if ai, ok := authInfoFrom(ctx); ok {
		// Store the versions before the flag: a version interceptor that sees
		// rejected set can read both.
		ai.rejectedClientVersion.Store(got)
		ai.rejectedBrokerVersion.Store(v)
		ai.rejected.Store(true)
	}
	return nil, toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_VERSION_MISMATCH,
		"local protocol version %d is not supported by this Broker (version %d); restart the Broker", got, v))
}
