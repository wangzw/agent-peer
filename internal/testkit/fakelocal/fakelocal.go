// Package fakelocal provides a scriptable fake of the Broker's Local gRPC
// service for tests.
//
// A Server listens on a Unix socket, answers every RPC from a Handlers value,
// records the unary requests it received, and lets a test take control of each
// Register stream. It never starts a Broker and depends on no part of the
// Broker implementation beyond the generated localpb package.
//
// The package is intended to be imported from test files. The plan for the
// mcpserver unit and later units (cmd, version compatibility) extend it.
package fakelocal

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// SocketPath returns a short socket path in a fresh directory removed at
// cleanup. The directory name starts with "ap-" and the path stays well below
// the Unix socket limit of 108 bytes on Linux and 104 bytes on macOS.
func SocketPath(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ap-")
	if err != nil {
		t.Fatalf("fakelocal: creating the socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "broker.sock")
}

// Selector returns a termcmd-compatible selector for a Server at path. The
// returned value is the unnamed function type the selector interface uses, so
// a test package that may not import localpb can assign it directly.
func Selector(path string) func(context.Context) (string, func() (*grpc.ClientConn, localpb.LocalClient, error), error) {
	return func(context.Context) (string, func() (*grpc.ClientConn, localpb.LocalClient, error), error) {
		return path, func() (*grpc.ClientConn, localpb.LocalClient, error) {
			cc, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return nil, nil, err
			}
			return cc, localpb.NewLocalClient(cc), nil
		}, nil
	}
}

// Handlers holds optional per-RPC behaviour. A nil field answers
// codes.Unimplemented, except Hello, which answers localpb.ProtocolVersion.
type Handlers struct {
	Hello            func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error)
	ListAgents       func(context.Context, *localpb.ListAgentsRequest) (*localpb.ListAgentsResponse, error)
	Send             func(context.Context, *localpb.SendRequest) (*localpb.SendResponse, error)
	JoinTopic        func(context.Context, *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error)
	LeaveTopic       func(context.Context, *localpb.LeaveTopicRequest) (*localpb.LeaveTopicResponse, error)
	ListTopics       func(context.Context, *localpb.ListTopicsRequest) (*localpb.ListTopicsResponse, error)
	ListBrokers      func(context.Context, *localpb.ListBrokersRequest) (*localpb.ListBrokersResponse, error)
	Connect          func(context.Context, *localpb.ConnectRequest) (*localpb.ConnectResponse, error)
	Disconnect       func(context.Context, *localpb.DisconnectRequest) (*localpb.DisconnectResponse, error)
	CreatePairingKey func(context.Context, *localpb.CreatePairingKeyRequest) (*localpb.CreatePairingKeyResponse, error)
	GetConfig        func(context.Context, *localpb.GetConfigRequest) (*localpb.GetConfigResponse, error)
	SetConfig        func(context.Context, *localpb.SetConfigRequest) (*localpb.SetConfigResponse, error)
	Status           func(context.Context, *localpb.StatusRequest) (*localpb.StatusResponse, error)
	Audit            func(*localpb.AuditRequest, localpb.Local_AuditServer) error
}

// Call is one unary request a Server received, in arrival order.
// AdminHandlers returns handlers for the six Broker administration RPCs, each
// answering a sample whose formatted output is distinct: config get prints an
// entry, config set a change line, pairing-key create a key, peer list the
// empty listing, peer connect a paired line, and peer disconnect a
// disconnected line.
func AdminHandlers() Handlers {
	return Handlers{
		GetConfig: func(context.Context, *localpb.GetConfigRequest) (*localpb.GetConfigResponse, error) {
			return &localpb.GetConfigResponse{Entries: []*localpb.ConfigEntry{
				{Key: "broker.alias", Value: &localpb.ConfigValue{Value: &localpb.ConfigValue_StringValue{StringValue: "hostA"}}},
			}}, nil
		},
		SetConfig: func(context.Context, *localpb.SetConfigRequest) (*localpb.SetConfigResponse, error) {
			return &localpb.SetConfigResponse{
				OldValue: &localpb.ConfigValue{Value: &localpb.ConfigValue_StringValue{StringValue: "old"}},
				NewValue: &localpb.ConfigValue{Value: &localpb.ConfigValue_StringValue{StringValue: "new"}},
			}, nil
		},
		CreatePairingKey: func(context.Context, *localpb.CreatePairingKeyRequest) (*localpb.CreatePairingKeyResponse, error) {
			return &localpb.CreatePairingKeyResponse{Key: "one-time-key"}, nil
		},
		ListBrokers: func(context.Context, *localpb.ListBrokersRequest) (*localpb.ListBrokersResponse, error) {
			return &localpb.ListBrokersResponse{}, nil
		},
		Connect: func(context.Context, *localpb.ConnectRequest) (*localpb.ConnectResponse, error) {
			return &localpb.ConnectResponse{Alias: "hostB", Fingerprint: bytes.Repeat([]byte{0xcd}, 32)}, nil
		},
		Disconnect: func(context.Context, *localpb.DisconnectRequest) (*localpb.DisconnectResponse, error) {
			return &localpb.DisconnectResponse{}, nil
		},
	}
}

type Call struct {
	Method  string
	Request proto.Message
}

// Server is a fake Local service listening on a Unix socket.
type Server struct {
	t        testing.TB
	path     string
	handlers Handlers

	mu      sync.Mutex
	calls   []Call
	ready   []*RegisterConn
	streams map[*RegisterConn]struct{}
	notify  chan struct{}
	closed  chan struct{}
	stopped bool
	lis     net.Listener
	gs      *grpc.Server
	regWG   sync.WaitGroup
}

// Start listens on path and serves until Stop or test cleanup. When h.Hello is
// nil, Hello answers localpb.ProtocolVersion.
func Start(t testing.TB, path string, h Handlers) *Server {
	t.Helper()
	s := &Server{
		t:        t,
		path:     path,
		handlers: h,
		streams:  make(map[*RegisterConn]struct{}),
		notify:   make(chan struct{}, 1),
	}
	s.start()
	t.Cleanup(s.Stop)
	return s
}

func (s *Server) start() {
	lis, err := net.Listen("unix", s.path)
	if err != nil {
		s.t.Fatalf("fakelocal: listening on %s: %v", s.path, err)
	}
	gs := grpc.NewServer()
	localpb.RegisterLocalServer(gs, &service{s: s})

	s.mu.Lock()
	s.lis = lis
	s.gs = gs
	s.closed = make(chan struct{})
	s.stopped = false
	s.ready = nil
	s.mu.Unlock()

	go func() { _ = gs.Serve(lis) }()
}

// Path returns the socket path the server listens on.
func (s *Server) Path() string { return s.path }

// Stop closes the listener, ends every open Register stream with
// codes.Unavailable, stops the gRPC server and removes the socket file. It is
// idempotent.
func (s *Server) Stop() { s.stop() }

func (s *Server) stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	lis, gs := s.lis, s.gs
	streams := make([]*RegisterConn, 0, len(s.streams))
	for c := range s.streams {
		streams = append(streams, c)
	}
	close(s.closed)
	s.ready = nil
	s.mu.Unlock()

	if lis != nil {
		_ = lis.Close()
	}
	for _, c := range streams {
		c.End()
	}
	s.regWG.Wait()
	if gs != nil {
		gs.Stop()
	}
	_ = os.Remove(s.path)
}

// Restart stops the server like Stop and listens again on the same path with
// the same Handlers. Calls is not cleared. Used to simulate a Broker restart.
func (s *Server) Restart() {
	s.stop()
	s.start()
}

// NextRegister waits for the next Register stream whose first frame has
// arrived.
func (s *Server) NextRegister(ctx context.Context) (*RegisterConn, error) {
	for {
		s.mu.Lock()
		if len(s.ready) > 0 {
			c := s.ready[0]
			s.ready = s.ready[1:]
			s.mu.Unlock()
			return c, nil
		}
		closed := s.closed
		notify := s.notify
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-closed:
			return nil, status.Error(codes.Unavailable, "fakelocal: server stopped")
		case <-notify:
		}
	}
}

// Calls returns every unary request received so far, in arrival order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Server) record(method string, req proto.Message) {
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: method, Request: req})
	s.mu.Unlock()
}

func (s *Server) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// RegisterConn is one open Register stream, controlled by the test.
type RegisterConn struct {
	First *localpb.RegisterRequest

	s      *Server
	stream localpb.Local_RegisterServer
	done   chan struct{}
	end    chan struct{}
	endOne sync.Once
	sendMu sync.Mutex
}

// Send writes one response on the stream.
func (c *RegisterConn) Send(r *localpb.RegisterResponse) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-c.done:
		return status.Error(codes.Unavailable, "fakelocal: register stream ended")
	default:
	}
	return c.stream.Send(r)
}

// Complete sends a registration_complete event.
func (c *RegisterConn) Complete(agentID, address string, autoJoin bool) error {
	return c.Send(&localpb.RegisterResponse{
		Event: &localpb.RegisterResponse_RegistrationComplete{
			RegistrationComplete: &localpb.RegistrationComplete{
				AgentId:      agentID,
				AgentAddress: address,
				AutoJoin:     autoJoin,
			},
		},
	})
}

// NotRegistered sends a not_registered event carrying reason.
func (c *RegisterConn) NotRegistered(r localpb.NotRegisteredReason) error {
	return c.Send(&localpb.RegisterResponse{
		Event: &localpb.RegisterResponse_NotRegistered{
			NotRegistered: &localpb.RegistrationProblem{Reason: r},
		},
	})
}

// End makes the server end the stream with codes.Unavailable. It is safe to
// call more than once.
func (c *RegisterConn) End() {
	c.endOne.Do(func() { close(c.end) })
}

// Done is closed when the stream ends for any reason, the client ending it
// included.
func (c *RegisterConn) Done() <-chan struct{} { return c.done }

// ErrorStatus builds a gRPC status error with code carrying exactly one
// ErrorDetail.
func ErrorStatus(code codes.Code, detail *localpb.ErrorDetail) error {
	st := status.New(code, detail.GetMessage())
	st, err := st.WithDetails(detail)
	if err != nil {
		return status.Error(code, detail.GetMessage())
	}
	return st.Err()
}

type service struct {
	localpb.UnimplementedLocalServer
	s *Server
}

func unimplemented(method string) error {
	return status.Errorf(codes.Unimplemented, "fakelocal: %s not implemented", method)
}

func (svc *service) Register(stream localpb.Local_RegisterServer) error {
	s := svc.s

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return status.Error(codes.Unavailable, "fakelocal: server stopped")
	}
	s.regWG.Add(1)
	c := &RegisterConn{
		s:      s,
		stream: stream,
		done:   make(chan struct{}),
		end:    make(chan struct{}),
	}
	s.streams[c] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.streams, c)
		s.mu.Unlock()
		// Wait for an in-flight Send before closing Done. Send holds sendMu for
		// the whole write, and grpc-go forbids using a server stream after its
		// handler has returned.
		c.sendMu.Lock()
		close(c.done)
		c.sendMu.Unlock()
		s.regWG.Done()
	}()

	type recvResult struct {
		req *localpb.RegisterRequest
		err error
	}
	first := make(chan recvResult, 1)
	go func() {
		req, err := stream.Recv()
		first <- recvResult{req: req, err: err}
	}()

	select {
	case <-c.end:
		return status.Error(codes.Unavailable, "fakelocal: server ended the register stream")
	case <-stream.Context().Done():
		return status.Error(codes.Unavailable, "fakelocal: client ended the register stream")
	case r := <-first:
		if r.err != nil {
			return nil
		}
		c.First = r.req
		s.record("Register", r.req)
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return status.Error(codes.Unavailable, "fakelocal: server stopped")
	}
	s.ready = append(s.ready, c)
	s.mu.Unlock()
	s.signal()

	// The Broker accepts only the first frame; every later frame is read and
	// discarded until the stream ends.
	drained := make(chan struct{}, 1)
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				drained <- struct{}{}
				return
			}
		}
	}()

	select {
	case <-c.end:
		return status.Error(codes.Unavailable, "fakelocal: server ended the register stream")
	case <-stream.Context().Done():
		return status.Error(codes.Unavailable, "fakelocal: client ended the register stream")
	case <-drained:
		return nil
	}
}

func (svc *service) Hello(ctx context.Context, req *localpb.HelloRequest) (*localpb.HelloResponse, error) {
	svc.s.record("Hello", req)
	if h := svc.s.handlers.Hello; h != nil {
		return h(ctx, req)
	}
	return &localpb.HelloResponse{ProtocolVersion: localpb.ProtocolVersion}, nil
}

func (svc *service) ListAgents(ctx context.Context, req *localpb.ListAgentsRequest) (*localpb.ListAgentsResponse, error) {
	svc.s.record("ListAgents", req)
	if h := svc.s.handlers.ListAgents; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("ListAgents")
}

func (svc *service) Send(ctx context.Context, req *localpb.SendRequest) (*localpb.SendResponse, error) {
	svc.s.record("Send", req)
	if h := svc.s.handlers.Send; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("Send")
}

func (svc *service) JoinTopic(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
	svc.s.record("JoinTopic", req)
	if h := svc.s.handlers.JoinTopic; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("JoinTopic")
}

func (svc *service) LeaveTopic(ctx context.Context, req *localpb.LeaveTopicRequest) (*localpb.LeaveTopicResponse, error) {
	svc.s.record("LeaveTopic", req)
	if h := svc.s.handlers.LeaveTopic; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("LeaveTopic")
}

func (svc *service) ListTopics(ctx context.Context, req *localpb.ListTopicsRequest) (*localpb.ListTopicsResponse, error) {
	svc.s.record("ListTopics", req)
	if h := svc.s.handlers.ListTopics; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("ListTopics")
}

func (svc *service) ListBrokers(ctx context.Context, req *localpb.ListBrokersRequest) (*localpb.ListBrokersResponse, error) {
	svc.s.record("ListBrokers", req)
	if h := svc.s.handlers.ListBrokers; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("ListBrokers")
}

func (svc *service) Connect(ctx context.Context, req *localpb.ConnectRequest) (*localpb.ConnectResponse, error) {
	svc.s.record("Connect", req)
	if h := svc.s.handlers.Connect; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("Connect")
}

func (svc *service) Disconnect(ctx context.Context, req *localpb.DisconnectRequest) (*localpb.DisconnectResponse, error) {
	svc.s.record("Disconnect", req)
	if h := svc.s.handlers.Disconnect; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("Disconnect")
}

func (svc *service) CreatePairingKey(ctx context.Context, req *localpb.CreatePairingKeyRequest) (*localpb.CreatePairingKeyResponse, error) {
	svc.s.record("CreatePairingKey", req)
	if h := svc.s.handlers.CreatePairingKey; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("CreatePairingKey")
}

func (svc *service) GetConfig(ctx context.Context, req *localpb.GetConfigRequest) (*localpb.GetConfigResponse, error) {
	svc.s.record("GetConfig", req)
	if h := svc.s.handlers.GetConfig; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("GetConfig")
}

func (svc *service) SetConfig(ctx context.Context, req *localpb.SetConfigRequest) (*localpb.SetConfigResponse, error) {
	svc.s.record("SetConfig", req)
	if h := svc.s.handlers.SetConfig; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("SetConfig")
}

func (svc *service) Status(ctx context.Context, req *localpb.StatusRequest) (*localpb.StatusResponse, error) {
	svc.s.record("Status", req)
	if h := svc.s.handlers.Status; h != nil {
		return h(ctx, req)
	}
	return nil, unimplemented("Status")
}

func (svc *service) Audit(req *localpb.AuditRequest, stream localpb.Local_AuditServer) error {
	if h := svc.s.handlers.Audit; h != nil {
		return h(req, stream)
	}
	return unimplemented("Audit")
}
