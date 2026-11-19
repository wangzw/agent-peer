//go:build !linux && !darwin

package service

import (
	"fmt"
	"net"
)

// socketPeerPID is unavailable on platforms the service does not support.
func socketPeerPID(*net.UnixConn) (int, error) {
	return 0, fmt.Errorf("socket peer credentials are not supported on this platform")
}
