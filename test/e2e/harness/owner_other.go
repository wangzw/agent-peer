//go:build !linux

package harness

import "errors"

// processStartTime is unavailable where /proc does not exist; the owner then
// records zero and its PID alone decides whether it is alive.
func processStartTime(int) (uint64, error) {
	return 0, errors.ErrUnsupported
}
