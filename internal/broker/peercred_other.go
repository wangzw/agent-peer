//go:build !linux && !darwin

package broker

import (
	"errors"
	"net"
)

// readPeerCred is unavailable on platforms without process credentials for
// Unix sockets. A local interface there is treated as unusable rather than
// trusted.
func readPeerCred(*net.UnixConn) (Caller, error) {
	return Caller{}, errors.New("peer credentials are not supported on this platform")
}
