package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/wangzw/agent-peer/internal/broker"
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
}

func TestProtocolOverridesDefaultZero(t *testing.T) {
	t.Parallel()

	local, link := protocolOverrides()
	if local != 0 || link != 0 {
		t.Fatalf("protocolOverrides() = (%d, %d), want (0, 0)", local, link)
	}
}
