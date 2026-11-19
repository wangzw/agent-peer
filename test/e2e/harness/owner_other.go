//go:build !linux

package harness

import "errors"

// processStartTime is unavailable where /proc does not exist; the owner then
// records zero and its PID alone decides whether it is alive.
func processStartTime(int) (uint64, error) {
	return 0, errors.ErrUnsupported
}

// processNamespace is unavailable where /proc does not exist. A zero namespace
// records that the platform has no PID namespace to compare.
func processNamespace(int) (uint64, error) {
	return 0, errors.ErrUnsupported
}
