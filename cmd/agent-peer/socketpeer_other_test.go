//go:build !linux && !darwin

package main

import "errors"

// Without process credentials for Unix sockets the cleanup of a launched Broker
// cannot find its pid. Tests that launch one skip instead of leaving the
// process behind.
func init() { socketPeerPIDSupported = false }

// socketPeerPID is unavailable on platforms without process credentials for
// Unix sockets, so the cleanup of a launched Broker cannot find its pid.
func socketPeerPID(string) (int, error) {
	return 0, errors.New("socket peer credentials are not supported on this platform")
}
