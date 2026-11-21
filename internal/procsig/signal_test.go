package procsig

import (
	"os"
	"os/exec"
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

// TestAliveRefusesGuardPids checks that the liveness probe refuses the same
// pids SendSignal refuses. Alive must not send a signal for any of them.
func TestAliveRefusesGuardPids(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if Alive(pid) {
			t.Fatalf("Alive(%d) = true, want false", pid)
		}
	}
}

// TestAliveAcceptsOwnPID checks that the liveness probe reports a live pid, so
// the refusal above cannot pass by reporting every pid dead.
func TestAliveAcceptsOwnPID(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatalf("Alive(%d) = false, want true", os.Getpid())
	}
}

// TestAliveReportsExitedProcess checks that a pid stops counting as alive once
// its process has exited and been reaped.
func TestAliveReportsExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting true: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("waiting for true: %v", err)
	}
	if Alive(pid) {
		t.Fatalf("Alive(%d) = true after the process exited", pid)
	}
}
