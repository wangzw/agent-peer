//go:build !linux

package node

import "errors"

// InboxConnections is only implemented on Linux, where /proc/net/unix is
// available.
func (s *Session) InboxConnections() (int, error) {
	return 0, errors.ErrUnsupported
}
