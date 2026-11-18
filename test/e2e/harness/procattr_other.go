//go:build !linux

package harness

import "syscall"

// setParentDeath is a no-op where the platform has no parent-death signal.
func setParentDeath(*syscall.SysProcAttr) {}
