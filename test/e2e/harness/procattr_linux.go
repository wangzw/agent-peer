//go:build linux

package harness

import "syscall"

// setParentDeath makes the kernel kill the child when the process that started
// it dies. A supervisor that is killed must not leave a test run behind.
func setParentDeath(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
