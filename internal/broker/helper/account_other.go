//go:build !linux

package helper

import "syscall"

// setParentDeathSignal is a no-op where the kernel has no parent death signal.
func setParentDeathSignal(*syscall.SysProcAttr) {}
