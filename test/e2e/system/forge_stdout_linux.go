//go:build e2e && linux

package e2e

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openProcessStdout returns a writable file for pid's standard output. It
// first tries /proc/<pid>/fd/1, the path the forged-response attack names. The
// stdout pipe belongs to the root Broker that created it, so an unprivileged
// caller is refused there even when it may inspect the process; the same file
// description is then duplicated with pidfd_getfd, which is gated by ptrace
// access instead of the pipe inode's mode.
func openProcessStdout(pid int) (*os.File, error) {
	path := fmt.Sprintf("/proc/%d/fd/1", pid)
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		return f, nil
	} else {
		targetErr := err

		pidfd, pidfdErr := unix.PidfdOpen(pid, 0)
		if pidfdErr != nil {
			return nil, fmt.Errorf("%v; pidfd_open: %w", targetErr, pidfdErr)
		}
		defer func() { _ = unix.Close(pidfd) }()

		fd, fdErr := unix.PidfdGetfd(pidfd, 1, 0)
		if fdErr != nil {
			return nil, fmt.Errorf("%v; pidfd_getfd: %w", targetErr, fdErr)
		}
		return os.NewFile(uintptr(fd), path), nil
	}
}
