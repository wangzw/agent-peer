package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// fakeLocal answers Hello and Status on a temporary local socket, standing in
// for a running Broker.
type fakeLocal struct {
	localpb.UnimplementedLocalServer
	degraded *localpb.DegradedState
}

func (f *fakeLocal) Hello(context.Context, *localpb.HelloRequest) (*localpb.HelloResponse, error) {
	return &localpb.HelloResponse{ProtocolVersion: localpb.ProtocolVersion}, nil
}

func (f *fakeLocal) Status(context.Context, *localpb.StatusRequest) (*localpb.StatusResponse, error) {
	return &localpb.StatusResponse{Degraded: f.degraded}, nil
}

func startFakeLocal(t *testing.T, degraded *localpb.DegradedState) string {
	t.Helper()
	sock := shortProbeSocket(t)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	srv := grpc.NewServer()
	localpb.RegisterLocalServer(srv, &fakeLocal{degraded: degraded})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return sock
}

// shortProbeSocket keeps the Unix socket path under the sun_path limit. On
// macOS t.TempDir plus the test name can exceed it, and bind then fails with
// EINVAL.
func shortProbeSocket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.sock")
	if len(path) <= 103 {
		return path
	}
	dir, err := os.MkdirTemp("/tmp", "ap-svc-sock-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "broker.sock")
}

func TestStatusProbeNormal(t *testing.T) {
	sock := startFakeLocal(t, nil)

	state, degraded, err := defaultStatusProbe(sock)
	if err != nil {
		t.Fatalf("defaultStatusProbe: %v", err)
	}
	if degraded {
		t.Errorf("degraded = true, want false for a normal Broker")
	}
	if state != (DegradedState{}) {
		t.Errorf("state = %+v, want the zero value", state)
	}
}

func TestStatusProbeLocalOnly(t *testing.T) {
	sock := startFakeLocal(t, &localpb.DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY,
		Step:   "listen",
		Reason: "broker.listen :17443 is in use",
		Fix:    "change broker.listen",
	})

	state, degraded, err := defaultStatusProbe(sock)
	if err != nil {
		t.Fatalf("defaultStatusProbe: %v", err)
	}
	if !degraded {
		t.Fatal("degraded = false, want true")
	}
	want := DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY,
		Step:   "listen",
		Reason: "broker.listen :17443 is in use",
		Fix:    "change broker.listen",
	}
	if state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}
}

func TestStatusProbeDiagnosticsOnly(t *testing.T) {
	sock := startFakeLocal(t, &localpb.DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY,
		Step:   "data directory",
		Reason: "data directory is not writable",
		Fix:    "fix the ownership of the data directory",
	})

	state, degraded, err := defaultStatusProbe(sock)
	if err != nil {
		t.Fatalf("defaultStatusProbe: %v", err)
	}
	if !degraded {
		t.Fatal("degraded = false, want true")
	}
	want := DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY,
		Step:   "data directory",
		Reason: "data directory is not writable",
		Fix:    "fix the ownership of the data directory",
	}
	if state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}
}

func TestStatusProbeNoAnswer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "not-listening.sock")

	state, degraded, err := defaultStatusProbe(sock)
	if err == nil {
		t.Fatal("defaultStatusProbe succeeded although nothing answers on the socket")
	}
	if degraded {
		t.Error("degraded = true without an answer")
	}
	if state != (DegradedState{}) {
		t.Errorf("state = %+v, want the zero value without an answer", state)
	}
}

func TestStatusProbeRejectsForeignPeer(t *testing.T) {
	sock := startFakeLocal(t, nil)
	me := uint32(os.Geteuid())
	statusPeerUID = func(net.Conn) (uint32, error) { return me + 1, nil }
	t.Cleanup(func() { statusPeerUID = systemPeerUID })

	state, degraded, err := defaultStatusProbe(sock)
	if err == nil {
		t.Fatal("defaultStatusProbe accepted a socket served by another account")
	}
	if degraded {
		t.Error("degraded = true for a socket that must be rejected")
	}
	if state != (DegradedState{}) {
		t.Errorf("state = %+v, want the zero value for a rejected socket", state)
	}
}

func TestStatusPeerAllowed(t *testing.T) {
	me := uint32(os.Geteuid())
	foreign := me + 1
	tests := []struct {
		name string
		path string
		uid  uint32
		want bool
	}{
		{"system socket served by root", "/run/agent-peer/broker.sock", 0, true},
		{"system socket served by another account", "/run/agent-peer/broker.sock", foreign, false},
		{"darwin system socket served by root", "/var/run/agent-peer/broker.sock", 0, true},
		{"darwin system socket served by this account", "/var/run/agent-peer/broker.sock", me, me == 0},
		{"user socket served by this account", filepath.Join("/tmp", "agent-peer-"+strconv.Itoa(int(me)), "broker.sock"), me, true},
		{"user socket served by another account", filepath.Join("/tmp", "agent-peer-x", "broker.sock"), foreign, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusPeerAllowed(tt.path, tt.uid); got != tt.want {
				t.Errorf("statusPeerAllowed(%q, %d) = %t, want %t", tt.path, tt.uid, got, tt.want)
			}
		})
	}
}

func TestDegradedErrorIncludesReason(t *testing.T) {
	err := (&DegradedError{State: DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY,
		Reason: "data directory is not writable",
	}})
	msg := err.Error()
	for _, want := range []string{"degraded", "diagnostics only", "data directory is not writable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, does not contain %q", msg, want)
		}
	}
}

func TestDegradedErrorWithoutReason(t *testing.T) {
	err := (&DegradedError{})
	if got := err.Error(); got == "" || strings.HasSuffix(got, ": ") {
		t.Errorf("Error() = %q, want a non-empty sentence without a dangling reason", got)
	}
}

func TestDefaultOptionsSetsStatusProbe(t *testing.T) {
	opts, err := DefaultOptions(false)
	if err != nil {
		t.Fatalf("DefaultOptions: %v", err)
	}
	if opts.StatusProbe == nil {
		t.Fatal("DefaultOptions left StatusProbe nil")
	}

	sock := startFakeLocal(t, &localpb.DegradedState{
		Level:  localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY,
		Step:   "listen",
		Reason: "broker.listen :17443 is in use",
	})
	state, degraded, err := opts.StatusProbe(sock)
	if err != nil {
		t.Fatalf("StatusProbe: %v", err)
	}
	if !degraded || state.Level != localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY || state.Step != "listen" {
		t.Errorf("StatusProbe = %+v, degraded %t; want the fake Broker's local-only state", state, degraded)
	}
}
