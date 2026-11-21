// Package procsig sends signals to processes while refusing a pid that does
// not name exactly one process. A pid can stop naming a process while code
// still holds it: Process.Release sets Pid to -1, a service manager may report
// MainPID 0 before the process starts, and os.Getppid returns 1 once the caller
// is adopted by init.
package procsig

import (
	"fmt"
	"syscall"
)

// SendSignal sends sig to pid after checking that pid names one process. It
// refuses 1, 0 and negative values: kill(1, sig) targets init, kill(0, sig)
// targets the caller's process group, and kill(-1, sig) targets every process
// the caller may signal. A cleanup that loses its own pid must fail loudly
// instead of reaching any of those.
func SendSignal(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d: a signal must name one process above pid 1", pid)
	}
	return syscall.Kill(pid, sig)
}
