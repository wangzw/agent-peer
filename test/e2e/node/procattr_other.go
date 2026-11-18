//go:build !linux

package node

import "syscall"

// setDeathSignal is a no-op where the platform has no parent-death signal.
func setDeathSignal(*syscall.SysProcAttr) {}
