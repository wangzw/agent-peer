//go:build darwin

package service

import "syscall"

// detachedSysProcAttr puts the helper in a new session, so the launchd job's
// bootout, which stops its process group, cannot reach the helper that
// performs the bootstrap.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
