//go:build e2e_darwin

package darwinservice

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/peers/test/e2e/harness"
)

const (
	// controlUserPort is the user Broker's listen port in these tests. It is
	// outside the kernel's ephemeral range and unused by the other service
	// tests.
	controlUserPort = "17448"
	// controlObservationWindow is the acceptance window of
	// REQ-broker-config-063. The job's RestartSec equivalent is launchd's
	// 10-second ThrottleInterval, so 30 seconds covers three restart attempts.
	controlObservationWindow = 30 * time.Second
)

// TestUserServiceControl stops, starts and restarts the user service and
// checks launchd's view after each step. Stop unloads the job but keeps the
// plist, so the service still starts at the next login.
func TestUserServiceControl(t *testing.T) {
	resetUserService(t)
	t.Cleanup(func() { resetUserService(t) })

	domain := userDomain(t)
	writeUserConfig(t, "127.0.0.1:"+controlUserPort)
	if res := run(t, installedBinary, "service", "install"); res.exitCode != 0 {
		t.Fatalf("service install: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	first := waitForPID(t, domain, serviceStartBound)
	plist := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", serviceLabel+".plist")

	res := run(t, installedBinary, "service", "stop")
	if res.exitCode != 0 {
		t.Fatalf("service stop: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The user Broker service has stopped.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	// Stop waits for launchd to unload the job, so the service must already be
	// gone from launchd's view when the command returns. Polling here instead
	// would mask a missing wait.
	assertUnloaded(t, domain)
	waitFor(t, serviceStopBound, "the stopped Broker process to exit", func() (bool, string) {
		return !processAlive(first), fmt.Sprintf("pid %d is still running", first)
	})
	if _, err := os.Stat(plist); err != nil {
		t.Errorf("plist %s after service stop: %v; stop must not remove it", plist, err)
	}

	res = run(t, installedBinary, "service", "start")
	if res.exitCode != 0 {
		t.Fatalf("service start: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The user Broker service has started.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	second := waitForNewPID(t, domain, first, serviceStartBound)
	t.Logf("service start replaced pid %d with pid %d", first, second)

	res = run(t, installedBinary, "service", "restart")
	if res.exitCode != 0 {
		t.Fatalf("service restart: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The user Broker service has restarted.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	third := waitForNewPID(t, domain, second, restartBound)
	t.Logf("service restart replaced pid %d with pid %d", second, third)
}

// assertUnloaded checks that launchd no longer knows the service in domain. The
// service commands wait for the unload before returning, so this runs once
// instead of polling, which would hide a missing wait.
func assertUnloaded(t *testing.T, domain string) {
	t.Helper()
	if out, loaded := launchctlPrint(domain); loaded {
		t.Errorf("launchd still reports the service in %s after the command returned: %s", domain, strings.TrimSpace(out))
	}
}

// TestUserServiceRestartWithoutPlist checks that restart refuses to unload a
// running service whose plist was deleted. Installed accepts the loaded job,
// but a restart that booted it out first could not bootstrap the missing file
// and would stop the service for good.
func TestUserServiceRestartWithoutPlist(t *testing.T) {
	resetUserService(t)
	t.Cleanup(func() { resetUserService(t) })

	domain := userDomain(t)
	writeUserConfig(t, "127.0.0.1:"+controlUserPort)
	if res := run(t, installedBinary, "service", "install"); res.exitCode != 0 {
		t.Fatalf("service install: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	pid := waitForPID(t, domain, serviceStartBound)
	plist := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", serviceLabel+".plist")
	if err := os.Remove(plist); err != nil {
		t.Fatalf("remove %s: %v", plist, err)
	}

	res := run(t, installedBinary, "service", "restart")
	if res.exitCode != 1 {
		t.Fatalf("service restart without a plist: exit %d, want 1\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	for _, want := range []string{"plist", "service install"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", strings.TrimSpace(res.stderr), want)
		}
	}
	if got := launchdPID(domain); got != pid {
		t.Errorf("launchd pid = %d after the refused restart, want the unchanged %d", got, pid)
	}
	if !processAlive(pid) {
		t.Errorf("pid %d did not survive the refused restart", pid)
	}
}

// TestSystemServiceControl is the system counterpart: stop unloads the
// daemon from launchd but keeps the plist, and start and restart bring it back
// with a new pid.
func TestSystemServiceControl(t *testing.T) {
	resetSystemService(t)
	t.Cleanup(func() { resetSystemService(t) })

	if res := run(t, "sudo", "-n", installedBinary, "service", "install", "--system"); res.exitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	first := waitForPID(t, "system", serviceStartBound)

	res := run(t, "sudo", "-n", installedBinary, "service", "stop")
	if res.exitCode != 0 {
		t.Fatalf("service stop as root: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The system Broker service has stopped.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	// Stop waits for launchd to unload the job, so the service must already be
	// gone from launchd's view when the command returns.
	assertUnloaded(t, "system")
	waitFor(t, serviceStopBound, "the stopped system Broker process to exit", func() (bool, string) {
		return !processAlive(first), fmt.Sprintf("pid %d is still running", first)
	})
	if _, err := os.Stat(daemonPlist); err != nil {
		t.Errorf("plist %s after service stop: %v; stop must not remove it", daemonPlist, err)
	}

	res = run(t, "sudo", "-n", installedBinary, "service", "start")
	if res.exitCode != 0 {
		t.Fatalf("service start as root: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The system Broker service has started.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	second := waitForNewPID(t, "system", first, serviceStartBound)

	res = run(t, "sudo", "-n", installedBinary, "service", "restart")
	if res.exitCode != 0 {
		t.Fatalf("service restart as root: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	if want := "The system Broker service has restarted.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	waitForNewPID(t, "system", second, restartBound)
}

// TestServiceControlNotInstalled checks the messages for a level without a
// service and for an account that only has the system installation.
func TestServiceControlNotInstalled(t *testing.T) {
	resetUserService(t)
	resetSystemService(t)
	t.Cleanup(func() {
		resetUserService(t)
		resetSystemService(t)
	})

	if res := run(t, "sudo", "-n", installedBinary, "service", "install", "--system"); res.exitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	waitForPID(t, "system", serviceStartBound)

	res := run(t, installedBinary, "service", "start")
	if res.exitCode != 1 {
		t.Fatalf("service start with only a system install: exit %d, want 1\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	for _, want := range []string{"no user service is installed", "system service is installed", "sudo peers service start --system"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", strings.TrimSpace(res.stderr), want)
		}
	}

	if res := run(t, "sudo", "-n", installedBinary, "service", "remove", "--system"); res.exitCode != 0 {
		t.Fatalf("service remove --system: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	waitForGone(t, "system", serviceStopBound)

	res = run(t, "sudo", "-n", installedBinary, "service", "start")
	if res.exitCode != 1 {
		t.Fatalf("service start as root with nothing installed: exit %d, want 1\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	for _, want := range []string{"no system service is installed", "sudo peers service install --system"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", strings.TrimSpace(res.stderr), want)
		}
	}
}

// TestUserServiceControlDegraded covers the service-start half of
// REQ-broker-config-063: with the listen address taken, start leaves the
// service running degraded, reports it, and launchd keeps the same pid.
func TestUserServiceControlDegraded(t *testing.T) {
	resetUserService(t)
	t.Cleanup(func() { resetUserService(t) })

	domain := userDomain(t)
	writeUserConfig(t, "127.0.0.1:"+controlUserPort)
	plist := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", serviceLabel+".plist")

	if res := run(t, installedBinary, "service", "install"); res.exitCode != 0 {
		t.Fatalf("service install: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	plistData, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("reading the installed plist: %v", err)
	}
	if !strings.Contains(string(plistData), "--allow-degraded") {
		t.Fatalf("the plist %s does not pass --allow-degraded:\n%s", plist, plistData)
	}
	pid := waitForPID(t, domain, serviceStartBound)

	if res := run(t, installedBinary, "service", "stop"); res.exitCode != 0 {
		t.Fatalf("service stop: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	waitForGone(t, domain, serviceStopBound)
	waitFor(t, serviceStopBound, "the stopped Broker process to exit", func() (bool, string) {
		return !processAlive(pid), fmt.Sprintf("pid %d is still running", pid)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:"+controlUserPort)
	if err != nil {
		t.Fatalf("holding the user Broker's port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	res := run(t, installedBinary, "service", "start")
	if res.exitCode != 3 {
		t.Fatalf("service start with the port taken: exit %d, want 3\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	if want := "The user Broker service has started.\n"; res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	status := run(t, installedBinary, "service", "status")
	if status.exitCode != 0 {
		t.Fatalf("peers service status in local-only degradation: exit %d\nstdout: %s\nstderr: %s",
			status.exitCode, strings.TrimSpace(status.stdout), strings.TrimSpace(status.stderr))
	}
	for _, want := range []string{"Broker: degraded (local only):", "Fix:"} {
		if !strings.Contains(status.stdout, want) {
			t.Errorf("status stdout = %q, want it to contain %q", strings.TrimSpace(status.stdout), want)
		}
	}

	stable := waitForPID(t, domain, serviceStartBound)
	t.Logf("the degraded user Broker runs with pid %d", stable)
	harness.Consistently(t, controlObservationWindow, "launchd to keep the degraded Broker's pid", func() (bool, string) {
		current := launchdPID(domain)
		return current == stable, fmt.Sprintf("pid = %d, want the unchanged %d", current, stable)
	})
}
