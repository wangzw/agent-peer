//go:build e2e

package syscontainer

import (
	"os"
	"strings"
	"testing"
)

// StartSystemService installs and starts the system-level Broker through
// systemd. When configYAML is non-empty it is written to the system
// configuration file (owner root, mode 0600) before the service starts. The
// service is stopped when the test ends. Use Run(t, Systemd) to get a container
// whose process 1 is systemd.
func StartSystemService(t *testing.T, configYAML string) {
	t.Helper()
	if !InContainer() {
		t.Fatalf("syscontainer: StartSystemService runs inside the container")
	}
	if !systemdIsPID1() {
		t.Fatalf("syscontainer: StartSystemService needs the systemd container; systemd is not process 1")
	}

	install := RunAs(t, "root", nil, "service", "install", "--system")
	if install.ExitCode != 0 {
		t.Fatalf("syscontainer: service install --system: exit %d\n%s\n%s",
			install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
	}
	if configYAML != "" {
		writeSystemConfig(t, configYAML)
	}

	start := Command("root", nil, "systemctl", "start", "agent-peer.service")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("syscontainer: systemctl start agent-peer.service: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	WaitFor(t, "the system service socket "+systemSocketPath, func() bool {
		_, err := os.Stat(systemSocketPath)
		return err == nil
	})

	t.Cleanup(func() {
		stop := Command("root", nil, "systemctl", "stop", "agent-peer.service")
		if out, err := stop.CombinedOutput(); err != nil {
			t.Logf("syscontainer: systemctl stop agent-peer.service: %v\n%s", err, strings.TrimSpace(string(out)))
		}
	})
}

// systemdIsPID1 reports whether systemd runs as the container's init process.
func systemdIsPID1() bool {
	stat, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(stat)) == "systemd"
}
