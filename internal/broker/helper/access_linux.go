//go:build linux

package helper

import "golang.org/x/sys/unix"

// faccessat2 checks path for mode as the process's effective identity. The
// process switched its effective ids to the account, and AT_EACCESS makes the
// kernel judge those ids. Calling faccessat2 directly avoids the permission-bit
// fallback x/sys applies when faccessat2 is missing or refused.
func faccessat2(path string, mode uint32) error {
	return unix.Faccessat2(unix.AT_FDCWD, path, mode, unix.AT_EACCESS)
}
