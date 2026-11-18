package termcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// UnaryTimeout bounds each unary call to the Local service. The Audit stream
// has no overall deadline; it ends when the command is interrupted.
const UnaryTimeout = 10 * time.Second

// Dialer opens a client connection to the Broker on the selected socket. The
// production value checks the peer before gRPC uses the connection. It is an
// alias so that a function returning this signature directly satisfies
// Selector.
type Dialer = func() (*grpc.ClientConn, localpb.LocalClient, error)

// Selector returns the socket path of a running Broker together with a function
// that opens a connection to it. It never starts a Broker.
// mcpserver.FindBrokerWithDial satisfies it.
type Selector func(ctx context.Context) (socketPath string, dial Dialer, err error)

// RunStatus connects to the selected Broker, checks that both ends speak the
// same local protocol version and writes the Broker status to w.
func RunStatus(ctx context.Context, sel Selector, w io.Writer) error {
	client, closeConn, err := dial(ctx, sel)
	if err != nil {
		return err
	}
	defer closeConn()

	callCtx, cancel := context.WithTimeout(ctx, UnaryTimeout)
	defer cancel()

	resp, err := client.Status(callCtx, &localpb.StatusRequest{})
	if err != nil {
		return errors.New(Describe(err))
	}
	return WriteStatus(w, resp)
}

// RunAudit streams the audit log from the selected Broker to w, one line per
// event as it arrives. limit is passed to the server, where zero means no
// limit. Events written before a stream error stay in w.
func RunAudit(ctx context.Context, sel Selector, limit uint32, w io.Writer) error {
	client, closeConn, err := dial(ctx, sel)
	if err != nil {
		return err
	}
	defer closeConn()

	stream, err := client.Audit(ctx, &localpb.AuditRequest{Limit: limit})
	if err != nil {
		return errors.New(Describe(err))
	}
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New(Describe(err))
		}
		if err := WriteAuditEvent(w, ev); err != nil {
			return err
		}
	}
}

// dial selects the Broker, opens a connection through the selector's dialer and
// performs the version handshake. The returned close function closes the
// connection.
func dial(ctx context.Context, sel Selector) (localpb.LocalClient, func(), error) {
	path, open, err := sel(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach the Broker: %w", err)
	}

	conn, client, err := open()
	if err != nil {
		return nil, nil, errors.New(Describe(err))
	}
	closeConn := func() { _ = conn.Close() }

	callCtx, cancel := context.WithTimeout(ctx, UnaryTimeout)
	defer cancel()

	resp, err := client.Hello(callCtx, &localpb.HelloRequest{ProtocolVersion: localpb.ProtocolVersion})
	if err != nil {
		closeConn()
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			return nil, nil, fmt.Errorf("cannot reach the Broker at %s: %s", path, st.Message())
		}
		return nil, nil, errors.New(Describe(err))
	}
	if v := resp.GetProtocolVersion(); v != localpb.ProtocolVersion {
		closeConn()
		return nil, nil, &VersionMismatchError{Broker: v, Client: localpb.ProtocolVersion}
	}
	return client, closeConn, nil
}
