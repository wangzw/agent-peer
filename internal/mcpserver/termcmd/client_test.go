package termcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
)

// fixedSelector returns path and an unchecked dialer from every call.
func fixedSelector(path string) Selector {
	return func(context.Context) (string, Dialer, error) {
		return path, func() (*grpc.ClientConn, localpb.LocalClient, error) {
			cc, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return nil, nil, err
			}
			return cc, localpb.NewLocalClient(cc), nil
		}, nil
	}
}

// methodCalls counts the unary calls srv received for method.
func methodCalls(srv *fakelocal.Server, method string) int {
	n := 0
	for _, c := range srv.Calls() {
		if c.Method == method {
			n++
		}
	}
	return n
}

// within runs fn and fails the test if it has not returned within d.
func within(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("the call did not return within %s", d)
		return nil
	}
}

func TestRunStatusUsesSelectedSocket(t *testing.T) {
	path := fakelocal.SocketPath(t)
	resp := &localpb.StatusResponse{
		Agents: []*localpb.AgentStatusEntry{{
			Address: "alice@host-a",
			Account: "wangzw",
			Status:  localpb.AgentStatus_AGENT_STATUS_IDLE,
			Topics:  []string{"team-a"},
		}},
	}
	srv := fakelocal.Start(t, path, fakelocal.Handlers{
		Status: func(context.Context, *localpb.StatusRequest) (*localpb.StatusResponse, error) {
			return resp, nil
		},
	})

	var got bytes.Buffer
	if err := RunStatus(t.Context(), fixedSelector(path), &got); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}

	var want bytes.Buffer
	if err := WriteStatus(&want, resp); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	if got.String() != want.String() {
		t.Errorf("RunStatus output = %q, want %q", got.String(), want.String())
	}
	if n := methodCalls(srv, "Hello"); n != 1 {
		t.Errorf("Hello calls = %d, want 1", n)
	}
	if n := methodCalls(srv, "Status"); n != 1 {
		t.Errorf("Status calls = %d, want 1", n)
	}
}

func TestRunStatusSelectorError(t *testing.T) {
	want := "cannot reach the Broker: no running Broker found"
	var got bytes.Buffer
	err := RunStatus(t.Context(), func(context.Context) (string, Dialer, error) {
		return "", nil, errors.New("no running Broker found")
	}, &got)
	if err == nil || err.Error() != want {
		t.Fatalf("RunStatus error = %v, want %q", err, want)
	}
	if got.Len() != 0 {
		t.Errorf("RunStatus wrote %q, want nothing", got.String())
	}
}

func TestRunStatusUnreachable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sock")
	var got bytes.Buffer
	err := within(t, 3*time.Second, func() error {
		return RunStatus(t.Context(), fixedSelector(path), &got)
	})
	if err == nil {
		t.Fatal("RunStatus returned nil, want an error")
	}
	if prefix := "cannot reach the Broker at " + path; !strings.HasPrefix(err.Error(), prefix) {
		t.Errorf("RunStatus error = %q, want it to start with %q", err, prefix)
	}
	if got.Len() != 0 {
		t.Errorf("RunStatus wrote %q, want nothing", got.String())
	}
}

func TestRunStatusVersionMismatch(t *testing.T) {
	path := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, path, fakelocal.Handlers{
		Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
			return &localpb.HelloResponse{ProtocolVersion: 2}, nil
		},
	})

	var got bytes.Buffer
	err := RunStatus(t.Context(), fixedSelector(path), &got)
	var vm *VersionMismatchError
	if !errors.As(err, &vm) {
		t.Fatalf("RunStatus error = %v, want *VersionMismatchError", err)
	}
	if vm.Broker != 2 || vm.Client != localpb.ProtocolVersion {
		t.Errorf("VersionMismatchError = %+v, want Broker 2 and Client %d", vm, localpb.ProtocolVersion)
	}
	if n := methodCalls(srv, "Status"); n != 0 {
		t.Errorf("Status calls = %d, want 0", n)
	}
}

func TestRunStatusHelloVersionMismatchError(t *testing.T) {
	path := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, path, fakelocal.Handlers{
		Hello: func(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
			return nil, fakelocal.ErrorStatus(codes.FailedPrecondition, &localpb.ErrorDetail{
				Code:    localpb.ErrorCode_ERROR_CODE_VERSION_MISMATCH,
				Message: "local protocol version 1 is not supported by this Broker (version 2); restart the Broker",
			})
		},
	})

	var got bytes.Buffer
	err := RunStatus(t.Context(), fixedSelector(path), &got)
	if err == nil {
		t.Fatal("RunStatus returned nil, want an error")
	}
	if !strings.HasPrefix(err.Error(), "version_mismatch: ") {
		t.Errorf("RunStatus error = %q, want it to start with %q", err, "version_mismatch: ")
	}
	if !strings.Contains(err.Error(), "with this binary") {
		t.Errorf("RunStatus error = %q, want it to say to restart the Broker with this binary", err)
	}
	if n := methodCalls(srv, "Status"); n != 0 {
		t.Errorf("Status calls = %d, want 0", n)
	}
}

func TestRunStatusPermissionDenied(t *testing.T) {
	path := fakelocal.SocketPath(t)
	fakelocal.Start(t, path, fakelocal.Handlers{
		Status: func(context.Context, *localpb.StatusRequest) (*localpb.StatusResponse, error) {
			return nil, fakelocal.ErrorStatus(codes.PermissionDenied, &localpb.ErrorDetail{
				Code:    localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
				Message: "read permission on the audit log is required",
			})
		},
	})

	var got bytes.Buffer
	err := RunStatus(t.Context(), fixedSelector(path), &got)
	if err == nil {
		t.Fatal("RunStatus returned nil, want an error")
	}
	if !strings.HasPrefix(err.Error(), "permission_denied: ") {
		t.Errorf("RunStatus error = %q, want it to start with %q", err, "permission_denied: ")
	}
}

// sampleAuditEvents builds five events in arrival order.
func sampleAuditEvents() []*localpb.AuditEvent {
	const base = "2026-11-18T10:00:00Z"
	start, err := time.Parse(time.RFC3339, base)
	if err != nil {
		panic(err)
	}
	events := make([]*localpb.AuditEvent, 5)
	for i := range events {
		events[i] = &localpb.AuditEvent{
			Time:  timestamppb.New(start.Add(time.Duration(i) * time.Minute)),
			Event: localpb.AuditEventKind_AUDIT_EVENT_KIND_CONFIG_SET,
			Actor: fmt.Sprintf("actor%d", i),
		}
	}
	return events
}

// auditRecorder records the Limit of each Audit request in a concurrency-safe
// way.
type auditRecorder struct {
	mu   sync.Mutex
	reqs []uint32
}

func (r *auditRecorder) record(limit uint32) {
	r.mu.Lock()
	r.reqs = append(r.reqs, limit)
	r.mu.Unlock()
}

func (r *auditRecorder) limits() []uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint32(nil), r.reqs...)
}

func TestRunAuditStreamsWithLimit(t *testing.T) {
	events := sampleAuditEvents()

	tests := []struct {
		name  string
		limit uint32
		want  []*localpb.AuditEvent
	}{
		{name: "limit", limit: 2, want: events[len(events)-2:]},
		{name: "no limit", limit: 0, want: events},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := fakelocal.SocketPath(t)
			rec := &auditRecorder{}
			fakelocal.Start(t, path, fakelocal.Handlers{
				Audit: func(req *localpb.AuditRequest, stream localpb.Local_AuditServer) error {
					rec.record(req.GetLimit())
					evs := events
					if n := int(req.GetLimit()); n > 0 && n < len(evs) {
						evs = evs[len(evs)-n:]
					}
					for _, ev := range evs {
						if err := stream.Send(ev); err != nil {
							return err
						}
					}
					return nil
				},
			})

			var got bytes.Buffer
			if err := RunAudit(t.Context(), fixedSelector(path), tt.limit, &got); err != nil {
				t.Fatalf("RunAudit: %v", err)
			}

			var want bytes.Buffer
			for _, ev := range tt.want {
				if err := WriteAuditEvent(&want, ev); err != nil {
					t.Fatalf("WriteAuditEvent: %v", err)
				}
			}
			if got.String() != want.String() {
				t.Errorf("RunAudit output = %q, want %q", got.String(), want.String())
			}
			if lines := strings.Count(got.String(), "\n"); lines != len(tt.want) {
				t.Errorf("RunAudit wrote %d lines, want %d", lines, len(tt.want))
			}
			if got := rec.limits(); len(got) != 1 || got[0] != tt.limit {
				t.Errorf("Audit request limits = %v, want [%d]", got, tt.limit)
			}
		})
	}
}

func TestRunAuditPermissionDenied(t *testing.T) {
	path := fakelocal.SocketPath(t)
	fakelocal.Start(t, path, fakelocal.Handlers{
		Audit: func(*localpb.AuditRequest, localpb.Local_AuditServer) error {
			return fakelocal.ErrorStatus(codes.PermissionDenied, &localpb.ErrorDetail{
				Code:    localpb.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
				Message: "read permission on the audit log is required",
			})
		},
	})

	var got bytes.Buffer
	err := RunAudit(t.Context(), fixedSelector(path), 0, &got)
	if err == nil {
		t.Fatal("RunAudit returned nil, want an error")
	}
	if !strings.HasPrefix(err.Error(), "permission_denied: ") {
		t.Errorf("RunAudit error = %q, want it to start with %q", err, "permission_denied: ")
	}
	if got.Len() != 0 {
		t.Errorf("RunAudit wrote %q, want nothing", got.String())
	}
}

func TestRunAuditErrorAfterEvents(t *testing.T) {
	path := fakelocal.SocketPath(t)
	events := sampleAuditEvents()[:2]
	fakelocal.Start(t, path, fakelocal.Handlers{
		Audit: func(_ *localpb.AuditRequest, stream localpb.Local_AuditServer) error {
			for _, ev := range events {
				if err := stream.Send(ev); err != nil {
					return err
				}
			}
			return status.Error(codes.Internal, "x")
		},
	})

	var got bytes.Buffer
	err := RunAudit(t.Context(), fixedSelector(path), 0, &got)
	if err == nil {
		t.Fatal("RunAudit returned nil, want an error")
	}

	var want bytes.Buffer
	for _, ev := range events {
		if err := WriteAuditEvent(&want, ev); err != nil {
			t.Fatalf("WriteAuditEvent: %v", err)
		}
	}
	if got.String() != want.String() {
		t.Errorf("RunAudit output = %q, want the two lines written before the error %q", got.String(), want.String())
	}
}

func TestRunAuditCancel(t *testing.T) {
	path := fakelocal.SocketPath(t)
	sent := make(chan struct{})
	events := sampleAuditEvents()[:1]
	fakelocal.Start(t, path, fakelocal.Handlers{
		Audit: func(_ *localpb.AuditRequest, stream localpb.Local_AuditServer) error {
			if err := stream.Send(events[0]); err != nil {
				return err
			}
			close(sent)
			<-stream.Context().Done()
			return nil
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var got bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- RunAudit(ctx, fixedSelector(path), 0, &got) }()

	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("the Audit handler did not send its event within 3s")
	}

	cancel()
	select {
	case err := <-done:
		// The cancellation is the point of the test; the error itself is
		// the reported context error and is not asserted here.
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("RunAudit did not return within 3s after the context was cancelled")
	}
}
