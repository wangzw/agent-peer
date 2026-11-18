//go:build linux

package main

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// socketPeerPID returns the pid of the process that accepts connections on the
// Unix socket at path. It exists for test cleanup: the MCP launcher starts the
// Broker with Setsid, so the test has no handle on the process and has to ask
// the socket who serves it.
func socketPeerPID(path string) (int, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("%s is not a Unix socket", path)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		pid   int
		opErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if credErr != nil {
			opErr = credErr
			return
		}
		pid = int(cred.Pid)
	}); err != nil {
		return 0, err
	}
	return pid, opErr
}
