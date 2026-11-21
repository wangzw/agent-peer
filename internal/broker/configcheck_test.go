package broker

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/peers/internal/config"
)

// TestListenProbeCheckRejectsBusyPort covers REQ-broker-config-034: an address
// that cannot be bound is rejected, and the successful probe of a busy port
// must not take the port away from its current holder.
func TestListenProbeCheckRejectsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()

	check := listenProbeCheck(config.ProbeListen)
	err = check(config.Value{}, config.Value{Kind: config.KindString, String: addr})
	if err == nil {
		t.Fatalf("check(%q) = nil, want an error for a busy port", addr)
	}
	for _, want := range []string{"is unavailable", addr} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("setting the accept deadline: %v", err)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dialing the occupied address after the check: %v", err)
	}
	_ = conn.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("the occupied listener no longer accepts: %v", err)
	}
}

// TestListenValidatorSameValueSkipsProbe covers the case where the running
// link holds the listener: setting broker.listen to the value it already has
// must succeed without probing, because a probe of the occupied address would
// fail. The probe fails the test if it is called at all.
func TestListenValidatorSameValueSkipsProbe(t *testing.T) {
	old := config.Value{Kind: config.KindString, String: "127.0.0.1:17443"}
	newValue := config.Value{Kind: config.KindString, String: "127.0.0.1:17443"}
	check := listenProbeCheck(func(addr string) error {
		t.Fatalf("probe(%q) called for an unchanged value", addr)
		return nil
	})
	if err := check(old, newValue); err != nil {
		t.Fatalf("check(unchanged value) = %v, want nil", err)
	}
}

// TestListenProbeCheckClosesProbe checks that a successful check releases its
// probe listener so the address can actually be bound afterwards.
func TestListenProbeCheckClosesProbe(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	check := listenProbeCheck(config.ProbeListen)
	if err := check(config.Value{}, config.Value{Kind: config.KindString, String: addr}); err != nil {
		t.Fatalf("check(%q) = %v, want nil for a free port", addr, err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("binding %q after the check: %v (the probe kept its listener)", addr, err)
	}
	_ = ln.Close()
}
