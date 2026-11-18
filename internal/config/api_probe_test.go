package config

import (
	"net"
	"testing"
)

// unusedTCPAddr returns a loopback address that is free at the time of the
// call. The port can be bound again between the probe and the real listener
// because no connection is ever accepted on it.
func unusedTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the loopback port: %v", err)
	}
	return addr
}

// C4: the broker.listen validator only probes. ProbeListen must close the
// listener it opened, so the port stays free for the real listener.
func TestProbeListenLeavesNoListener(t *testing.T) {
	addr := unusedTCPAddr(t)
	if err := ProbeListen(addr); err != nil {
		t.Fatalf("ProbeListen(%q) error = %v", addr, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("%q cannot be bound after a successful probe: %v", addr, err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	// An occupied address is rejected without disturbing the listener that
	// occupies it.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	if err := ProbeListen(occupied.Addr().String()); err == nil {
		t.Fatalf("ProbeListen(%q) accepted an occupied address", occupied.Addr())
	}

	// An empty broker.listen means "do not listen"; anything that broker.listen
	// rejects is rejected here too.
	if err := ProbeListen(""); err != nil {
		t.Fatalf("ProbeListen(\"\") error = %v, want nil for a disabled listener", err)
	}
	if err := ProbeListen("no-port"); err == nil {
		t.Fatal("ProbeListen accepted an address that broker.listen rejects")
	}
}

// REQ-broker-config-034: the broker.listen validator is a probe, so a
// successful Set leaves the port free. Running Set twice probes twice, and the
// file write that follows the probe finds no listener of ours holding the port.
func TestREQBrokerConfig034_ListenValidatorLeavesPortFree(t *testing.T) {
	c, _ := newValueCore(t, "broker:\n  listen: \":17443\"\n", "")
	installCoreForTest(t, c)

	addr := unusedTCPAddr(t)
	Validate("broker.listen", func(old, new Value) error { return ProbeListen(new.String) })

	for i := 0; i < 2; i++ {
		if _, err := Set(Actor{}, "broker.listen", stringValue(addr)); err != nil {
			t.Fatalf("Set(broker.listen) %d error = %v", i+1, err)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("%q is still occupied after Set validated it: %v", addr, err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
}
