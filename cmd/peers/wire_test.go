package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/broker"
	"github.com/wangzw/peers/internal/testkit/fakelocal"
)

func TestRealDepsAllWired(t *testing.T) {
	t.Parallel()

	d := realDeps("v")
	if d.runBroker == nil {
		t.Error("runBroker is nil")
	}
	if d.classifyBrokerErr == nil {
		t.Error("classifyBrokerErr is nil")
	}
	if d.runMCP == nil {
		t.Error("runMCP is nil")
	}
	if d.runStatus == nil {
		t.Error("runStatus is nil")
	}
	if d.runAudit == nil {
		t.Error("runAudit is nil")
	}
	if d.runConfigGet == nil {
		t.Error("runConfigGet is nil")
	}
	if d.runConfigSet == nil {
		t.Error("runConfigSet is nil")
	}
	if d.runPairingKeyCreate == nil {
		t.Error("runPairingKeyCreate is nil")
	}
	if d.runPeerList == nil {
		t.Error("runPeerList is nil")
	}
	if d.runPeerConnect == nil {
		t.Error("runPeerConnect is nil")
	}
	if d.runPeerDisconnect == nil {
		t.Error("runPeerDisconnect is nil")
	}
	if d.updateAPIBase == nil {
		t.Error("updateAPIBase is nil")
	}
	if d.updateLatest == nil {
		t.Error("updateLatest is nil")
	}
	if d.updateNewer == nil {
		t.Error("updateNewer is nil")
	}
	if d.updateApply == nil {
		t.Error("updateApply is nil")
	}
	if d.applyUpdateViaBroker == nil {
		t.Error("applyUpdateViaBroker is nil")
	}
	if d.brokerUpdateStatus == nil {
		t.Error("brokerUpdateStatus is nil")
	}
}

func TestClassifyBrokerErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sentinel error
		want     startFailure
	}{
		{broker.ErrAlreadyRunning, failAlreadyRunning},
		{broker.ErrListenUnavailable, failListenUnavailable},
		{broker.ErrInvalidConfig, failInvalidConfig},
		{broker.ErrInSession, failInSession},
		{broker.ErrNotRoot, failNotRoot},
		{broker.ErrRuntimeDir, failRuntimeDir},
		{broker.ErrInvalidListen, failInvalidListen},
		{broker.ErrLockLost, failOther},
	}
	for _, tt := range tests {
		t.Run(tt.sentinel.Error(), func(t *testing.T) {
			t.Parallel()

			// Run wraps every sentinel with its cause, so classification has
			// to survive wrapping.
			err := fmt.Errorf("%w: reason", tt.sentinel)
			if got := classifyBrokerErr(err); got != tt.want {
				t.Fatalf("classifyBrokerErr(%v) = %v, want %v", err, got, tt.want)
			}
		})
	}

	if got := classifyBrokerErr(errors.New("x")); got != failOther {
		t.Fatalf("classifyBrokerErr(unclassified) = %v, want failOther", got)
	}
	// A privileged-port refusal is a listen failure now; it shares the listen
	// exit code rather than the configuration one.
	lowPort := fmt.Errorf("%w: a listen port below 1024 requires root", broker.ErrListenUnavailable)
	if got := classifyBrokerErr(lowPort); got != failListenUnavailable {
		t.Fatalf("classifyBrokerErr(low port) = %v, want failListenUnavailable", got)
	}
	if got := exitCodeFor(failListenUnavailable); got != 4 {
		t.Fatalf("the listen failure exit code = %d, want 4", got)
	}
	if got := classifyBrokerErr(nil); got != failOther {
		t.Fatalf("classifyBrokerErr(nil) = %v, want failOther", got)
	}
}

func TestRealDepsBrokerOptions(t *testing.T) {
	// The brokerRun variable is package state; keep this test serial.
	original := brokerRun
	t.Cleanup(func() { brokerRun = original })

	var got []broker.RunOptions
	brokerRun = func(_ context.Context, opts broker.RunOptions) error {
		got = append(got, opts)
		return nil
	}

	d := realDeps("v")
	if err := d.runBroker(context.Background(), brokerOptions{}); err != nil {
		t.Fatalf("runBroker: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("broker.Run called %d times, want 1", len(got))
	}
	if got[0].Ready != nil {
		t.Fatalf("RunOptions.Ready = %#v, want a nil interface", got[0].Ready)
	}
	if got[0].System {
		t.Fatal("RunOptions.System = true, want false")
	}

	if err := d.runBroker(context.Background(), brokerOptions{System: true, Listen: ":17444"}); err != nil {
		t.Fatalf("runBroker with system mode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("broker.Run called %d times, want 2", len(got))
	}
	if !got[1].System || got[1].Listen != ":17444" {
		t.Fatalf("RunOptions = %+v, want system mode on :17444", got[1])
	}

	if err := d.runBroker(context.Background(), brokerOptions{AllowDegraded: true}); err != nil {
		t.Fatalf("runBroker with allow-degraded: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("broker.Run called %d times, want 3", len(got))
	}
	if !got[2].AllowDegraded {
		t.Fatal("RunOptions.AllowDegraded = false, want true")
	}
}

// TestRealDepsCallTheirTermcmdFunction checks that every dep reaches the
// termcmd function its name promises. The two commands without arguments,
// runPeerList and runPairingKeyCreate, share a signature, so a swap between
// them would compile; the fake Broker's distinct output and method name catch
// it.
func TestRealDepsCallTheirTermcmdFunction(t *testing.T) {
	path := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, path, fakelocal.AdminHandlers())

	original := findBroker
	findBroker = fakelocal.Selector(path)
	t.Cleanup(func() { findBroker = original })

	d := realDeps("v")
	ctx := t.Context()

	tests := []struct {
		name     string
		method   string
		wantHead string
		run      func(w io.Writer) error
	}{
		{
			name: "runConfigGet", method: "GetConfig", wantHead: "broker.alias = hostA\n",
			run: func(w io.Writer) error { return d.runConfigGet(ctx, "broker.alias", w) },
		},
		{
			name: "runConfigSet", method: "SetConfig", wantHead: "broker.alias: old -> new\n",
			run: func(w io.Writer) error { return d.runConfigSet(ctx, "broker.alias", "new", w) },
		},
		{
			name: "runPairingKeyCreate", method: "CreatePairingKey", wantHead: "Key: one-time-key\n",
			run: func(w io.Writer) error { return d.runPairingKeyCreate(ctx, w) },
		},
		{
			name: "runPeerList", method: "ListBrokers", wantHead: "Brokers: none\n",
			run: func(w io.Writer) error { return d.runPeerList(ctx, w) },
		},
		{
			name: "runPeerConnect", method: "Connect", wantHead: "Paired with hostB (",
			run: func(w io.Writer) error {
				return d.runPeerConnect(ctx, "hostb.example:17443", "one-time-key", "", w, io.Discard)
			},
		},
		{
			name: "runPeerDisconnect", method: "Disconnect", wantHead: "Disconnected hostB\n",
			run: func(w io.Writer) error { return d.runPeerDisconnect(ctx, "hostB", w) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := wireMethodCalls(srv, tt.method)
			var out bytes.Buffer
			if err := tt.run(&out); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if !strings.HasPrefix(out.String(), tt.wantHead) {
				t.Fatalf("%s wrote %q, want it to start with %q", tt.name, out.String(), tt.wantHead)
			}
			if got := wireMethodCalls(srv, tt.method) - before; got != 1 {
				t.Fatalf("%s called %s %d times, want 1", tt.name, tt.method, got)
			}
		})
	}
}

// wireMethodCalls counts the unary calls srv received for method.
func wireMethodCalls(srv *fakelocal.Server, method string) int {
	n := 0
	for _, call := range srv.Calls() {
		if call.Method == method {
			n++
		}
	}
	return n
}

func TestProtocolOverridesDefaultZero(t *testing.T) {
	t.Parallel()

	local, link := protocolOverrides()
	if local != 0 || link != 0 {
		t.Fatalf("protocolOverrides() = (%d, %d), want (0, 0)", local, link)
	}
}
