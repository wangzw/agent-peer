//go:build unix

package update

import (
	"os"
	"syscall"
)

// chownFile gives f the ownership recorded in info, so the replacement keeps
// the owner of the binary it replaces. A non-root process updating its own
// binary passes its own owner and is allowed to set the same values.
func chownFile(f *os.File, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return f.Chown(int(st.Uid), int(st.Gid))
}
