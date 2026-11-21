//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/peers/test/e2e/syscontainer"
)

// TestSystemServiceInstallListenPortConflict covers the field report behind
// the installation's listen-port preflight. When another process already holds
// the address in broker.listen, service install --system must stop before it
// registers anything and say which address, which configuration file and which
// key are at fault. A reinstall over a running service must still succeed,
// because the port is then held by the service's own Broker.
//
// Both subtests share one systemd container; each resets the service before and
// after itself, so a failed installation under the old behavior cannot leak
// into the next one.
func TestSystemServiceInstallListenPortConflict(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	t.Run("an occupied port fails the install and registers nothing", func(t *testing.T) {
		resetSystemServiceState(t)
		t.Cleanup(func() { resetSystemServiceState(t) })

		addr := occupiedListenAddr(t)
		writeSystemListenConfig(t, addr)

		res := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
		combined := res.Stdout + res.Stderr
		if res.ExitCode == 0 {
			t.Fatalf("service install --system accepted broker.listen %s held by another process\nstdout: %s\nstderr: %s",
				addr, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
		}
		for _, want := range []string{addr, "already in use", systemConfigPath, "broker.listen"} {
			if !strings.Contains(combined, want) {
				t.Errorf("service install --system error does not mention %q (exit %d)\nstdout: %s\nstderr: %s",
					want, res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
			}
		}
		assertSystemServiceNotRegistered(t)
	})

	t.Run("reinstalling while the service runs succeeds", func(t *testing.T) {
		resetSystemServiceState(t)
		t.Cleanup(func() { resetSystemServiceState(t) })

		// A fixed port outside the kernel's ephemeral range keeps the port free
		// between choosing it and the service binding it. The container runs this
		// test alone, and 17447 is unused by the other system tests.
		addr := "127.0.0.1:" + reinstallListenPort
		writeSystemListenConfig(t, addr)

		install := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
		if install.ExitCode != 0 {
			t.Fatalf("first service install --system on %s: exit %d\nstdout: %s\nstderr: %s",
				addr, install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
		}
		syscontainer.WaitFor(t, "the system Broker socket "+systemSocket, func() bool {
			_, err := os.Stat(systemSocket)
			return err == nil
		})

		// The service's own Broker now holds addr. The preflight must recognize
		// that occupant instead of reporting the reinstall as a port conflict.
		again := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
		if again.ExitCode != 0 {
			t.Fatalf("reinstalling service --system over the running service: exit %d\nstdout: %s\nstderr: %s",
				again.ExitCode, strings.TrimSpace(again.Stdout), strings.TrimSpace(again.Stderr))
		}
		enabled := runCommandAs(t, "root", "systemctl", "is-enabled", "peers.service")
		if state := strings.TrimSpace(enabled.Stdout); state != "enabled" {
			t.Errorf("systemctl is-enabled peers.service after the reinstall = %q (exit %d), want %q\nstderr: %s",
				state, enabled.ExitCode, "enabled", strings.TrimSpace(enabled.Stderr))
		}
		syscontainer.WaitFor(t, "the reinstalled system Broker to answer on "+addr, func() bool {
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		})
	})
}

// reinstallListenPort is the system Broker's listen port in the reinstall
// subtest. It is outside the kernel's ephemeral range and unused by the other
// system tests.
const reinstallListenPort = "17447"

// occupiedListenAddr holds a loopback TCP listener for the rest of the test and
// returns its address. Nothing else can take the address while the listener
// stays open, so the installation's preflight bind is guaranteed to fail.
func occupiedListenAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a loopback port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().String()
}

// writeSystemListenConfig writes the system Broker's configuration file with
// broker.listen set to addr, so the installation reads that address.
func writeSystemListenConfig(t *testing.T, addr string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(systemConfigPath), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(systemConfigPath), err)
	}
	content := fmt.Sprintf("broker:\n  listen: %q\n", addr)
	if err := os.WriteFile(systemConfigPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", systemConfigPath, err)
	}
	if err := os.Chmod(systemConfigPath, 0o600); err != nil {
		t.Fatalf("chmod %s: %v", systemConfigPath, err)
	}
	if err := os.Chown(systemConfigPath, 0, 0); err != nil {
		t.Fatalf("chown %s: %v", systemConfigPath, err)
	}
}

// resetSystemServiceState removes the unit and stops the Broker. The old
// behavior left an enabled unit restarting after a failed installation, so the
// stop and disable are separate from the product's uninstall command.
func resetSystemServiceState(t *testing.T) {
	t.Helper()

	_ = syscontainer.RunAs(t, "root", nil, "service", "remove", "--system")
	_ = runCommandAs(t, "root", "systemctl", "stop", "peers.service")
	_ = runCommandAs(t, "root", "systemctl", "disable", "peers.service")
	_ = runCommandAs(t, "root", "systemctl", "daemon-reload")
	_ = os.Remove(systemUnitPath)
}

// assertSystemServiceNotRegistered checks that systemd knows no enabled or
// running unit and that no system Broker process survived the failed
// installation.
func assertSystemServiceNotRegistered(t *testing.T) {
	t.Helper()

	enabled := runCommandAs(t, "root", "systemctl", "is-enabled", "peers.service")
	if state := strings.TrimSpace(enabled.Stdout); state == "enabled" {
		t.Errorf("systemctl is-enabled peers.service = %q after the failed installation, want it not enabled\nstderr: %s",
			state, strings.TrimSpace(enabled.Stderr))
	}
	active := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	switch state := strings.TrimSpace(active.Stdout); state {
	case "active", "activating", "reloading", "deactivating":
		t.Errorf("systemctl is-active peers.service = %q after the failed installation, want it inactive", state)
	}
	if procs := syscontainer.Pids("peers", "service", "run", "--system"); len(procs) != 0 {
		t.Errorf("the system Broker still runs after the failed installation: %v", procs)
	}
	// The unit is written only after the listen precheck, so a fresh
	// installation that cannot use its address leaves no unit file at all.
	if _, err := os.Stat(systemUnitPath); err == nil {
		t.Errorf("the unit file %s exists after the precheck failed", systemUnitPath)
	}
}
