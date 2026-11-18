package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/wangzw/agent-peer/internal/broker"
	"github.com/wangzw/agent-peer/internal/mcpserver"
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
	if d.runLauncher == nil {
		t.Error("runLauncher is nil")
	}
	if d.runStatus == nil {
		t.Error("runStatus is nil")
	}
	if d.runAudit == nil {
		t.Error("runAudit is nil")
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
		{broker.ErrSystemModeUnavailable, failSystemUnavailable},
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
	if got := classifyBrokerErr(nil); got != failOther {
		t.Fatalf("classifyBrokerErr(nil) = %v, want failOther", got)
	}
}

func TestReadyFDFlagMatchesMCPServer(t *testing.T) {
	t.Parallel()

	if got, want := "--"+readyFDFlagName, mcpserver.ReadyFDFlag; got != want {
		t.Fatalf("cmd flag %q does not match mcpserver.ReadyFDFlag %q", got, want)
	}
	if mcpserver.ReadyFD != 3 {
		t.Fatalf("mcpserver.ReadyFD = %d, want 3", mcpserver.ReadyFD)
	}
}

func TestLauncherKeyMatchesMCPServer(t *testing.T) {
	t.Parallel()

	key := strings.Join(mcpserver.LauncherArgs, " ")
	if _, ok := subcommands[key]; !ok {
		t.Fatalf("subcommands has no %q; mcpserver would launch an unknown command", key)
	}
}

func TestRealDepsReadyNilInterface(t *testing.T) {
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

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	if err := d.runBroker(context.Background(), brokerOptions{Ready: w, System: true}); err != nil {
		t.Fatalf("runBroker with ready pipe: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("broker.Run called %d times, want 2", len(got))
	}
	if got[1].Ready == nil {
		t.Fatal("RunOptions.Ready is nil although a readiness pipe was passed")
	}
	if !got[1].System {
		t.Fatal("RunOptions.System = false, want true")
	}
}

func TestProtocolOverridesDefaultZero(t *testing.T) {
	t.Parallel()

	local, link := protocolOverrides()
	if local != 0 || link != 0 {
		t.Fatalf("protocolOverrides() = (%d, %d), want (0, 0)", local, link)
	}
}
