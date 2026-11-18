//go:build linux

package mcpserver

import "golang.org/x/sys/unix"

// peerUIDFromFD reads the peer credentials of a connected socket with
// SO_PEERCRED.
func peerUIDFromFD(fd int) (uint32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
