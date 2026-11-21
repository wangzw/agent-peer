//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/peers/test/e2e/harness"
	"github.com/wangzw/peers/test/e2e/syscontainer"
)

// serviceRestartBound bounds the wait for systemd to replace a SIGKILLed
// system Broker. The drop-in below sets RestartSec to 100ms, so the bound is
// set by systemd's restart, not by the unit's default five seconds.
const serviceRestartBound = 3 * time.Second

// TestSystemServiceStartsAndRestarts starts the installed system service,
// kills the Broker with SIGKILL, and checks that systemd starts a new process
// with a different non-zero MainPID. This is the system-level counterpart of
// the user unit's restart check: a service manager, not a session, owns the
// Broker's lifetime.
func TestSystemServiceStartsAndRestarts(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	// RestartSec=5 in the unit would make the restart outlast the acceptance
	// bound. A drop-in in /run shortens it for this test only.
	dropInDir := "/run/systemd/system/agent-peer.service.d"
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dropInDir, err)
	}
	dropIn := filepath.Join(dropInDir, "restart.conf")
	content := "[Service]\nRestart=always\nRestartSec=100ms\n"
	if err := os.WriteFile(dropIn, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", dropIn, err)
	}
	if out, err := syscontainer.Command("root", nil, "systemctl", "daemon-reload").CombinedOutput(); err != nil {
		t.Fatalf("systemctl daemon-reload: %v\n%s", err, strings.TrimSpace(string(out)))
	}

	syscontainer.StartSystemService(t, "")
	before, ok := systemServiceMainPID(t)
	if !ok || before == 0 {
		t.Fatalf("systemctl show MainPID after the start = %d (found %v), want a non-zero pid", before, ok)
	}
	if err := syscall.Kill(before, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 the system Broker %d: %v", before, err)
	}
	t.Logf("the system Broker started with MainPID %d and was killed", before)

	var after int
	harness.Eventually(t, serviceRestartBound, "systemd to restart the system Broker",
		func() (bool, string) {
			pid, ok := systemServiceMainPID(t)
			if !ok {
				return false, "systemctl show did not report a MainPID"
			}
			if pid == 0 || pid == before {
				return false, fmt.Sprintf("MainPID = %d, want a new non-zero pid (was %d)", pid, before)
			}
			// systemd records the new MainPID when it forks the replacement,
			// before the child execs agent-peer. Until the exec the child still
			// carries systemd's command line, so the Broker listing can be empty
			// while MainPID already names the new process. Wait for the pid to
			// appear rather than scanning once after the wait.
			procs := syscontainer.Pids("agent-peer", "broker", "--system")
			if !hasPID(procs, pid) {
				return false, fmt.Sprintf("MainPID = %d, but the running system Brokers are %v", pid, procs)
			}
			after = pid
			return true, ""
		})
	t.Logf("systemd restarted the system Broker with MainPID %d", after)
}

// systemServiceMainPID returns the MainPID systemd reports for the system unit.
func systemServiceMainPID(t *testing.T) (int, bool) {
	t.Helper()
	cmd := syscontainer.Command("root", nil,
		"systemctl", "show", "--property", "MainPID", "--value", "agent-peer.service")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// hasPID reports whether procs contains pid.
func hasPID(procs []syscontainer.ProcInfo, pid int) bool {
	for _, proc := range procs {
		if proc.PID == pid {
			return true
		}
	}
	return false
}
