//go:build !darwin

package service

import "syscall"

// detachedSysProcAttr is only used by the macOS post-update helper, which this
// build never starts. It still sets Setsid so the property the unit test checks
// is the one the macOS build relies on, rather than a platform stub.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
