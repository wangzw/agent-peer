//go:build e2e && !linux

package e2e

import (
	"errors"
	"fmt"
	"os"
)

// openProcessStdout is only implemented on Linux; the attack role never runs
// on another platform.
func openProcessStdout(pid int) (*os.File, error) {
	return nil, fmt.Errorf("pidfd_getfd for pid %d: %w", pid, errors.ErrUnsupported)
}
