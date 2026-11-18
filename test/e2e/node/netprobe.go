package node

import (
	"fmt"
	"net"
	"time"
)

// HoldPort listens on addr and returns the listener, so a test can take a
// port for the duration of a scenario. The caller closes it.
func HoldPort(addr string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("node: listening on %s: %w", addr, err)
	}
	return listener, nil
}

// TCPReachable reports whether a TCP connection to addr succeeds within
// timeout.
func TCPReachable(addr string, timeout time.Duration) bool {
	conn, err := OpenConn(addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// OpenConn dials addr with timeout. It exists so that a fault injection can
// keep the connection and check whether it survives.
func OpenConn(addr string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("node: dialing %s: %w", addr, err)
	}
	return conn, nil
}
