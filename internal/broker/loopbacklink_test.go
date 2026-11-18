package broker

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/link/linkpb"
)

// maxLoopbackMessageSize is the gRPC message limit of the loopback link. It is
// the same as the link implementation's 4 MiB, so a frame that passes the
// too_large check has room on the wire.
const maxLoopbackMessageSize = 4 << 20

// loopbackLink implements deliveryLink over a real linkpb.Link gRPC stream on
// 127.0.0.1. The client side sends requests; the server side dispatches them to
// the registered handlers. Both ends share one object, so a delivery test can
// use the same value as the sender's link and as the receiver's link.
//
// It is test-only: production code must use the real *link.Link.
type loopbackLink struct {
	linkpb.UnimplementedLinkServer

	t      *testing.T
	remote peerID // identity attributed to requests arriving on the server

	mu       sync.Mutex
	handlers map[link.FrameKind]func(context.Context, peerID, link.Frame) link.Frame
	ups      []func(peerID, []byte)

	// stream is the current client stream, nil while the stream is down.
	stream       linkpb.Link_ConnectClient
	streamCancel context.CancelFunc
	streamDown   chan struct{}
	up           bool
	pending      map[uint64]chan *linkpb.Frame

	// sendMu serializes Send on both streams: gRPC allows one sender per
	// stream, and handler goroutines reply concurrently.
	sendMu sync.Mutex
	nextID atomic.Uint64

	lis  net.Listener
	srv  *grpc.Server
	conn *grpc.ClientConn
}

var _ deliveryLink = (*loopbackLink)(nil)

// newLoopbackLink starts a linkpb server on 127.0.0.1, connects a client, opens
// one stream and returns the link. Requests dispatched to handlers carry remote
// as the requesting peer.
func newLoopbackLink(t *testing.T, remote peerID) *loopbackLink {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback: listen: %v", err)
	}

	l := &loopbackLink{
		t:        t,
		remote:   remote,
		handlers: make(map[link.FrameKind]func(context.Context, peerID, link.Frame) link.Frame),
		pending:  make(map[uint64]chan *linkpb.Frame),
		lis:      lis,
	}
	l.srv = grpc.NewServer(
		grpc.MaxRecvMsgSize(maxLoopbackMessageSize),
		grpc.MaxSendMsgSize(maxLoopbackMessageSize),
	)
	linkpb.RegisterLinkServer(l.srv, l)
	go func() { _ = l.srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxLoopbackMessageSize),
			grpc.MaxCallSendMsgSize(maxLoopbackMessageSize),
		),
	)
	if err != nil {
		l.srv.Stop()
		t.Fatalf("loopback: dial: %v", err)
	}
	l.conn = conn

	t.Cleanup(func() {
		l.closeStream()
		_ = conn.Close()
		l.srv.Stop()
	})

	l.reopen()
	return l
}

// Connect serves the server side of the bidirectional stream: it hands every
// request frame to the registered handler in a new goroutine and answers an
// unregistered kind with the link's unsupported reply.
func (l *loopbackLink) Connect(stream linkpb.Link_ConnectServer) error {
	for {
		f, err := stream.Recv()
		if err != nil {
			return nil
		}
		if f.GetIsReply() {
			continue
		}
		l.dispatch(stream, f)
	}
}

// dispatch starts the handler for one request frame and sends its reply.
func (l *loopbackLink) dispatch(stream linkpb.Link_ConnectServer, f *linkpb.Frame) {
	handler := l.handler(link.FrameKind(f.GetKind()))
	if handler == nil {
		l.reply(stream, unsupportedLoopbackFrame(f))
		return
	}
	ctx := stream.Context()
	go func() {
		reply := handler(ctx, l.remote, link.Frame{
			Kind:    link.FrameKind(f.GetKind()),
			Payload: f.GetPayload(),
		})
		l.reply(stream, &linkpb.Frame{
			Kind:      reply.Kind,
			RequestId: f.GetRequestId(),
			IsReply:   true,
			Payload:   reply.Payload,
		})
	}()
}

// unsupportedLoopbackFrame builds the reply for a kind no handler registered.
func unsupportedLoopbackFrame(req *linkpb.Frame) *linkpb.Frame {
	payload, err := proto.Marshal(&linkpb.Unsupported{RequestKind: req.GetKind()})
	if err != nil {
		payload = nil
	}
	return &linkpb.Frame{
		Kind:      linkpb.KindUnsupported,
		RequestId: req.GetRequestId(),
		IsReply:   true,
		Payload:   payload,
	}
}

// reply sends f on the server stream. A stream closed by the test makes Send
// fail; the test relies on that, so the error is not reported.
func (l *loopbackLink) reply(stream linkpb.Link_ConnectServer, f *linkpb.Frame) {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	_ = stream.Send(f)
}

// Request sends a kind-400 frame and waits for its reply. The sent result
// follows the link contract: false before the frame was written, true once it
// was. A stream that is down before the write yields ErrNotEstablished; a
// stream that ends after the write yields ErrLinkDown.
func (l *loopbackLink) Request(ctx context.Context, _ peerID, f link.Frame) (link.Frame, bool, error) {
	l.mu.Lock()
	stream := l.stream
	down := l.streamDown
	up := l.up
	if !up || stream == nil {
		l.mu.Unlock()
		return link.Frame{}, false, fmt.Errorf("%w: loopback stream is closed", link.ErrNotEstablished)
	}
	id := l.nextID.Add(1)
	ch := make(chan *linkpb.Frame, 1)
	l.pending[id] = ch
	l.mu.Unlock()

	defer l.forget(id)

	l.sendMu.Lock()
	err := stream.Send(&linkpb.Frame{Kind: f.Kind, RequestId: id, Payload: f.Payload})
	l.sendMu.Unlock()
	if err != nil {
		return link.Frame{}, false, fmt.Errorf("%w: %v", link.ErrNotEstablished, err)
	}

	select {
	case reply := <-ch:
		if reply == nil {
			return link.Frame{}, true, link.ErrLinkDown
		}
		return loopbackReply(f.Kind, reply)
	case <-down:
		// A reply may have arrived just before the stream ended; prefer it.
		select {
		case reply := <-ch:
			if reply != nil {
				return loopbackReply(f.Kind, reply)
			}
		default:
		}
		return link.Frame{}, true, link.ErrLinkDown
	case <-ctx.Done():
		return link.Frame{}, true, ctx.Err()
	}
}

// loopbackReply converts a wire reply into the deliveryLink result.
func loopbackReply(kind link.FrameKind, reply *linkpb.Frame) (link.Frame, bool, error) {
	if reply.GetKind() == linkpb.KindUnsupported {
		refused := &linkpb.Unsupported{}
		if err := proto.Unmarshal(reply.GetPayload(), refused); err != nil {
			return link.Frame{}, true, &link.UnsupportedError{Kind: kind}
		}
		return link.Frame{}, true, &link.UnsupportedError{Kind: link.FrameKind(refused.GetRequestKind())}
	}
	return link.Frame{Kind: reply.GetKind(), Payload: reply.GetPayload()}, true, nil
}

// forget drops the pending entry of id. The receiver may already have removed
// it; deleting a missing key is harmless.
func (l *loopbackLink) forget(id uint64) {
	l.mu.Lock()
	delete(l.pending, id)
	l.mu.Unlock()
}

// recv reads replies for the client stream and wakes the matching Request.
func (l *loopbackLink) recv(stream linkpb.Link_ConnectClient, down chan struct{}) {
	for {
		f, err := stream.Recv()
		if err != nil {
			l.endStream(down)
			return
		}
		if !f.GetIsReply() {
			continue
		}
		l.mu.Lock()
		ch := l.pending[f.GetRequestId()]
		delete(l.pending, f.GetRequestId())
		l.mu.Unlock()
		if ch != nil {
			select {
			case ch <- f:
			default:
			}
		}
	}
}

// endStream marks down as the current stream ending: it clears the stream,
// cancels it and fails every waiting Request. A stale down channel from an
// earlier stream is ignored.
func (l *loopbackLink) endStream(down chan struct{}) {
	l.mu.Lock()
	if l.streamDown != down {
		l.mu.Unlock()
		return
	}
	l.up = false
	l.stream = nil
	l.streamDown = nil
	cancel := l.streamCancel
	l.streamCancel = nil
	pending := l.pending
	l.pending = make(map[uint64]chan *linkpb.Frame)
	close(down)
	l.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, ch := range pending {
		close(ch)
	}
}

// closeStream tears down the current client stream. A Request waiting on it
// returns sent true with ErrLinkDown, and Established turns false.
func (l *loopbackLink) closeStream() {
	l.mu.Lock()
	down := l.streamDown
	l.mu.Unlock()
	if down != nil {
		l.endStream(down)
	}
}

// reopen builds a new client stream and fires the onUp callbacks. It is the
// dialer's redial, so Kick calls it too.
func (l *loopbackLink) reopen() {
	l.mu.Lock()
	if l.up {
		l.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := linkpb.NewLinkClient(l.conn).Connect(ctx)
	if err != nil {
		l.mu.Unlock()
		cancel()
		l.t.Errorf("loopback: reopening the stream: %v", err)
		return
	}
	down := make(chan struct{})
	l.stream = stream
	l.streamCancel = cancel
	l.streamDown = down
	l.up = true
	l.pending = make(map[uint64]chan *linkpb.Frame)
	ups := append([]func(peerID, []byte){}, l.ups...)
	l.mu.Unlock()

	go l.recv(stream, down)
	for _, f := range ups {
		f(l.remote, nil)
	}
}

// HandleRequest registers the handler for kind.
func (l *loopbackLink) HandleRequest(kind link.FrameKind, h func(context.Context, peerID, link.Frame) link.Frame) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handlers[kind] = h
}

// handler returns the handler for kind, or nil when none is registered.
func (l *loopbackLink) handler(kind link.FrameKind) func(context.Context, peerID, link.Frame) link.Frame {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handlers[kind]
}

// Subscribe records the up callbacks. Down callbacks are recorded but never
// run: the tests wait for an explicit reopen.
func (l *loopbackLink) Subscribe(onUp func(peerID, []byte), onDown func(peerID)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if onUp != nil {
		l.ups = append(l.ups, onUp)
	}
	_ = onDown
}

// Established reports whether a client stream is open.
func (l *loopbackLink) Established(peerID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.up
}

// Kick redials: it reopens the stream and fires onUp, like the link's dialer.
func (l *loopbackLink) Kick(peerID) {
	l.reopen()
}
