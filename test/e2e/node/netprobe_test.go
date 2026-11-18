package node

import (
	"os"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestHoldPortAndReachable(t *testing.T) {
	listener, err := HoldPort("127.0.0.1:0")
	if err != nil {
		t.Fatalf("node: HoldPort: %v", err)
	}
	addr := listener.Addr().String()

	if !TCPReachable(addr, 2*time.Second) {
		t.Fatalf("node: %s is not reachable while the port is held", addr)
	}
	conn, err := OpenConn(addr, 2*time.Second)
	if err != nil {
		t.Fatalf("node: OpenConn: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("node: closing connection: %v", err)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("node: closing listener: %v", err)
	}

	// The port stops accepting as soon as the listener closes, but a
	// connection already in the kernel's backlog may take a moment to be
	// reset, so poll for a short window instead of asserting immediately.
	deadline := time.Now().Add(2 * time.Second)
	for TCPReachable(addr, 200*time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("node: %s is still reachable after the listener closed", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestListeningAddrs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("node: ListeningAddrs is implemented on Linux only")
	}

	listener, err := HoldPort("127.0.0.1:0")
	if err != nil {
		t.Fatalf("node: HoldPort: %v", err)
	}
	defer listener.Close()
	want := listener.Addr().String()

	addrs, err := ListeningAddrs(os.Getpid())
	if err != nil {
		t.Fatalf("node: ListeningAddrs: %v", err)
	}
	if !slices.Contains(addrs, want) {
		t.Fatalf("node: listening addresses %v do not contain %s", addrs, want)
	}

	if _, err := ListeningAddrs(-1); err == nil {
		t.Fatal("node: ListeningAddrs(-1) returned no error")
	}
}
