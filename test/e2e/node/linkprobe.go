package node

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/link/linkpb"
)

// ProbeHello dials addr over TLS with a fresh certificate, sends a Hello as
// the first frame, and returns the Reject reason. It is how a test asks a
// Broker what it does with a client that is not a paired peer.
//
// A stream that ends, or a ctx that expires, before a Reject arrives is an
// error: there was no answer to report.
func ProbeHello(ctx context.Context, addr string) (linkpb.RejectReason, error) {
	cert, err := NewSelfSignedCert()
	if err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: generate nonce: %w", err)
	}
	payload, err := proto.Marshal(&linkpb.Hello{
		ProtocolVersion: linkpb.ProtocolVersion,
		Alias:           "probe",
		Nonce:           nonce,
	})
	if err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: encode hello: %w", err)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(clientTLS(cert)))
	if err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	stream, err := linkpb.NewLinkClient(conn).Connect(ctx)
	if err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: open stream: %w", err)
	}
	if err := stream.Send(&linkpb.Frame{Kind: linkpb.KindHello, Payload: payload}); err != nil {
		return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: send hello: %w", err)
	}

	for {
		frame, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: stream ended before a reject: %w", err)
			}
			return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: no reject: %w", err)
		}
		if frame.GetKind() != linkpb.KindReject {
			continue
		}
		var reject linkpb.Reject
		if err := proto.Unmarshal(frame.GetPayload(), &reject); err != nil {
			return linkpb.RejectReason_REJECT_REASON_UNSPECIFIED, fmt.Errorf("node: probe hello: decode reject: %w", err)
		}
		return reject.GetReason(), nil
	}
}
