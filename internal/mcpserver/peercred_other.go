//go:build !linux && !darwin

package mcpserver

import "errors"

// peerUIDFromFD is unavailable on platforms without process credentials for
// Unix sockets. A Broker socket there is treated as unusable rather than trusted.
func peerUIDFromFD(int) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on this platform")
}
