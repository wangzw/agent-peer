//go:build !linux

package main

import (
	"errors"
	"net"
)

// peerPID reports that peer credentials are unavailable outside Linux. The
// callers still receive frames; only the sender's pid cannot be recorded.
func peerPID(conn net.Conn) (int, error) {
	return 0, errors.ErrUnsupported
}
