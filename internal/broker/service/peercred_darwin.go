//go:build darwin

package service

import "golang.org/x/sys/unix"

// peerUIDFromFD reads the peer credentials of a connected socket with
// LOCAL_PEERCRED.
func peerUIDFromFD(fd int) (uint32, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
