//go:build e2e && !linux

package e2e

import (
	"errors"
	"net"
)

// unixPeerUID is Linux-only. The test body runs inside the Linux container, so
// the host build only needs this stub to compile.
func unixPeerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("SO_PEERCRED is available only on linux")
}
