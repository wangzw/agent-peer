package helper

import "syscall"

// setParentDeathSignal makes the kernel kill the child when the Broker dies, so
// a helper cannot outlive it while holding the account's inbox socket.
func setParentDeathSignal(a *syscall.SysProcAttr) {
	a.Pdeathsig = syscall.SIGKILL
}
