package service

import (
	"fmt"
	"net"
)

// systemPeerUID returns the effective uid of the process at the other end of a
// connected Unix socket. The platform-specific read lives in peercred_*.go.
func systemPeerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("the connection is not a Unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		uid   uint32
		opErr error
	)
	if err := raw.Control(func(fd uintptr) {
		uid, opErr = peerUIDFromFD(int(fd))
	}); err != nil {
		return 0, err
	}
	return uid, opErr
}

// statusPeerUID reads the socket peer's account for the status probe. It is a
// package variable so tests can pretend a socket is served by another account.
var statusPeerUID = systemPeerUID
