//go:build !linux && !darwin

package main

import "errors"

// socketPeerPID is unavailable on platforms without process credentials for
// Unix sockets, so the cleanup of a launched Broker cannot find its pid.
func socketPeerPID(string) (int, error) {
	return 0, errors.New("socket peer credentials are not supported on this platform")
}
