//go:build !linux

package helper

import "golang.org/x/sys/unix"

// faccessat2 checks path for mode as the process's effective identity. Darwin
// has no separate faccessat2 system call, and faccessat with AT_EACCESS asks
// the kernel directly, with no permission-bit fallback.
func faccessat2(path string, mode uint32) error {
	return unix.Faccessat(unix.AT_FDCWD, path, mode, unix.AT_EACCESS)
}
