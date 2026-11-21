package procsig

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestSendSignalRefusesGuardPids checks the pids the guard exists for: -1
// reaches every process the account may signal, 0 reaches the caller's process
// group, and 1 reaches init. SIGCONT keeps a regression harmless while the
// message check still fails when the syscall is attempted and succeeds or
// returns a permission error.
func TestSendSignalRefusesGuardPids(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		err := SendSignal(pid, syscall.SIGCONT)
		if err == nil {
			t.Fatalf("SendSignal(%d) returned nil, want a refusal", pid)
		}
		if !strings.Contains(err.Error(), "refusing to signal pid") {
			t.Fatalf("SendSignal(%d) = %v, want a refusal", pid, err)
		}
	}
}

// TestSendSignalAcceptsOwnPID checks that the guard lets a live pid through,
// so the refusal above cannot pass by refusing every call.
func TestSendSignalAcceptsOwnPID(t *testing.T) {
	if err := SendSignal(os.Getpid(), syscall.SIGCONT); err != nil {
		t.Fatalf("SendSignal(%d, SIGCONT) = %v, want nil", os.Getpid(), err)
	}
}
