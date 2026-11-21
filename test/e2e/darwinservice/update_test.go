//go:build e2e_darwin

package darwinservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/peers/test/e2e/harness"
)

const (
	// updateOldVersion is the release the LaunchDaemon runs before the update.
	updateOldVersion = "0.1.0"
	// updateNewVersion is the release the fake GitHub API offers.
	updateNewVersion = "0.1.2"
	// updateAPIEnv is the test-build hook that points the Broker at the fake
	// GitHub API instead of api.github.com.
	updateAPIEnv = "AGENT_PEER_TEST_UPDATE_API"

	// updateRestartBound bounds the wait for launchd to start the restarted
	// Broker and for the test to read the new version. launchd applies its
	// default ThrottleInterval of 10 seconds before it relaunches a process
	// that exited too quickly, and the Broker's own startup and readiness take
	// a few seconds; the bound matches restartBound and leaves margin for a
	// loaded runner.
	updateRestartBound = 30 * time.Second

	// updateCommandBound bounds the update command. The command waits up to 60
	// seconds itself (updateRestartWait in cmd/peers) for the restarted
	// Broker to report the new version; this bound adds the restart margin so a
	// stuck relaunch fails with output instead of hanging the job.
	updateCommandBound = 90 * time.Second
)

// TestREQUpdate007_LaunchDaemonReplacedByApplyUpdate covers REQ-update-007 on
// macOS: a system-level LaunchDaemon replaces its own binary through
// ApplyUpdate, launchd starts the new binary under KeepAlive, the binary keeps
// its root ownership and permissions, and the restarted Broker still serves.
//
// The old release is built with the agentpeer_testenv tag and installed over
// the shared test binary, so the Broker reads the fake API base from its plist.
// The test restores the shared binary from a backup in t.Cleanup.
func TestREQUpdate007_LaunchDaemonReplacedByApplyUpdate(t *testing.T) {
	resetSystemService(t)
	preserveInstalledBinary(t)
	t.Cleanup(func() { resetSystemService(t) })
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if data, err := exec.Command("sudo", "-n", "cat", "/var/log/agent-peer/broker.log").Output(); err == nil {
			t.Logf("system Broker log:\n%s", data)
		}
	})

	old := harness.BuildBinaryVersion(t, updateOldVersion, "darwin", runtime.GOARCH, "agentpeer_testenv")
	installRootBinary(t, old, installedBinary)

	newBinary := harness.BuildBinaryVersion(t, updateNewVersion, "darwin", runtime.GOARCH)
	fake := harness.StartFakeGitHub(t, harness.NewRelease(t, updateNewVersion, "darwin", runtime.GOARCH, newBinary))

	res := run(t, "sudo", "-n", installedBinary, "service", "install", "--system")
	if res.exitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	pid := waitForPID(t, "system", serviceStartBound)
	t.Logf("launchd started the old release with pid %d", pid)

	beforeUID, beforeGID, beforeMode, err := binaryOwner(installedBinary)
	if err != nil {
		t.Fatalf("reading the owner of %s before the update: %v", installedBinary, err)
	}
	if beforeUID != 0 {
		t.Fatalf("%s is owned by uid %d before the update, want root", installedBinary, beforeUID)
	}

	// launchd hands the job's EnvironmentVariables to the process it starts.
	// The plist was written by service install, so the fake API base is added
	// to it and the job is bootstrapped again to start a Broker that sees it.
	addDaemonEnvironment(t, updateAPIEnv, fake.URL())
	bootout("system")
	waitForGone(t, "system", serviceStopBound)
	if err := sudo("launchctl", "bootstrap", "system", daemonPlist); err != nil {
		t.Fatalf("launchctl bootstrap system %s: %v", daemonPlist, err)
	}
	restarted := waitForNewPID(t, "system", pid, updateRestartBound)
	t.Logf("launchd restarted the old release with the test environment, pid %d", restarted)
	waitForBrokerVersion(t, updateOldVersion)

	// The command reports success only after the restarted Broker answers with
	// the version it was updated to.
	res = runWithin(t, updateCommandBound, "sudo", "-n", installedBinary, "update", "--yes")
	combined := res.stdout + res.stderr
	if res.exitCode != 0 {
		t.Fatalf("agent-peer update --yes: exit %d\nstdout:\n%s\nstderr:\n%s",
			res.exitCode, res.stdout, res.stderr)
	}
	// The command does not print the old version with --yes; the check before
	// the update already proved the old release was running.
	for _, want := range []string{
		"The Broker is restarting to version " + updateNewVersion,
		"The Broker is running agent-peer " + updateNewVersion,
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("the update output does not contain %q:\n%s", want, combined)
		}
	}
	if fake.Downloads() == 0 {
		t.Errorf("the update succeeded without downloading the release from the fake API")
	}

	if got := binaryVersion(t, installedBinary); got != "agent-peer "+updateNewVersion {
		t.Fatalf("the installed binary reports %q, want %q", got, "agent-peer "+updateNewVersion)
	}
	afterUID, afterGID, afterMode, err := binaryOwner(installedBinary)
	if err != nil {
		t.Fatalf("reading the owner of %s after the update: %v", installedBinary, err)
	}
	if afterUID != beforeUID || afterGID != beforeGID || afterMode != beforeMode {
		t.Errorf("the binary owner or mode changed by the update: before uid=%d gid=%d mode=%#o, after uid=%d gid=%d mode=%#o",
			beforeUID, beforeGID, beforeMode, afterUID, afterGID, afterMode)
	}

	// The restarted Broker reports the new version and still answers the local
	// interface, which is what "running the new release and serving" means.
	waitForBrokerVersion(t, updateNewVersion)
	if status := run(t, "sudo", "-n", installedBinary, "status"); status.exitCode != 0 {
		t.Fatalf("agent-peer status after the update: exit %d\nstdout: %s\nstderr: %s",
			status.exitCode, status.stdout, status.stderr)
	}

	audit := run(t, "sudo", "-n", installedBinary, "audit", "--limit", "50")
	if audit.exitCode != 0 {
		t.Fatalf("agent-peer audit after the update: exit %d\nstdout: %s\nstderr: %s",
			audit.exitCode, audit.stdout, audit.stderr)
	}
	if !strings.Contains(audit.stdout, "update_applied") {
		t.Errorf("the audit log carries no update_applied event:\n%s", audit.stdout)
	}
	for _, want := range []string{updateOldVersion, updateNewVersion} {
		if !strings.Contains(audit.stdout, want) {
			t.Errorf("the audit log does not name version %s:\n%s", want, audit.stdout)
		}
	}
}

// preserveInstalledBinary copies the shared test binary aside and restores it,
// with its root ownership and mode, when the test ends. The update test
// replaces that path with two release builds while the rest of the package
// expects the binary TestMain installed.
func preserveInstalledBinary(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(installedBinary)
	if err != nil {
		t.Fatalf("reading %s: %v", installedBinary, err)
	}
	backup, err := os.CreateTemp("", "agent-peer-test-binary-*")
	if err != nil {
		t.Fatalf("creating a backup of %s: %v", installedBinary, err)
	}
	if _, err := backup.Write(data); err != nil {
		_ = backup.Close()
		_ = os.Remove(backup.Name())
		t.Fatalf("writing the backup of %s: %v", installedBinary, err)
	}
	if err := backup.Close(); err != nil {
		_ = os.Remove(backup.Name())
		t.Fatalf("closing the backup of %s: %v", installedBinary, err)
	}
	t.Cleanup(func() {
		if err := sudo("install", "-o", "root", "-g", "wheel", "-m", "0755", backup.Name(), installedBinary); err != nil {
			t.Errorf("restoring %s: %v", installedBinary, err)
		}
		_ = os.Remove(backup.Name())
	})
}

// installRootBinary installs src at dst as a root-owned, world-executable
// binary, the ownership a system-level installation requires.
func installRootBinary(t *testing.T, src, dst string) {
	t.Helper()
	if err := sudo("install", "-o", "root", "-g", "wheel", "-m", "0755", src, dst); err != nil {
		t.Fatalf("installing %s as %s: %v", src, dst, err)
	}
}

// binaryOwner returns the owner and permission bits of path.
func binaryOwner(path string) (uid, gid uint32, mode os.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, fmt.Errorf("no owner information for %s", path)
	}
	return st.Uid, st.Gid, info.Mode().Perm(), nil
}

// binaryVersion runs path --version and returns its trimmed output.
func binaryVersion(t *testing.T, path string) string {
	t.Helper()
	res := run(t, path, "--version")
	if res.exitCode != 0 {
		t.Fatalf("%s --version: exit %d\nstderr: %s", path, res.exitCode, res.stderr)
	}
	return strings.TrimSpace(res.stdout)
}

// addDaemonEnvironment adds one EnvironmentVariables entry to the daemon
// plist. PlistBuddy writes the change in place; launchd reads the entry when
// the job is bootstrapped again.
func addDaemonEnvironment(t *testing.T, key, value string) {
	t.Helper()
	const plistBuddy = "/usr/libexec/PlistBuddy"
	if err := sudo(plistBuddy, "-c", "Add :EnvironmentVariables dict", daemonPlist); err != nil {
		t.Fatalf("adding EnvironmentVariables to %s: %v", daemonPlist, err)
	}
	if err := sudo(plistBuddy, "-c", "Add :EnvironmentVariables:"+key+" string "+value, daemonPlist); err != nil {
		t.Fatalf("adding %s to %s: %v", key, daemonPlist, err)
	}
}

// waitForBrokerVersion waits until the system Broker reports want.
func waitForBrokerVersion(t *testing.T, want string) {
	t.Helper()
	waitFor(t, updateRestartBound, "the system Broker to report version "+want, func() (bool, string) {
		got, err := systemBrokerVersion()
		if err != nil {
			return false, err.Error()
		}
		return got == want, "the Broker reports " + got
	})
}

// systemBrokerVersion reads the version the system Broker runs by executing the
// test binary's status role as root, where the system socket admits the caller.
func systemBrokerVersion() (string, error) {
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the test binary: %w", err)
	}
	cmd := exec.Command("sudo", "-n", "env", roleEnv+"="+systemUpdateStatusRole, binary)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("system-update-status: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("system-update-status: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runWithin runs name with args under a bound and returns its output and exit
// code. A process that does not finish within the bound fails the test with the
// output it produced.
func runWithin(t *testing.T, bound time.Duration, name string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return res
	}
	if ctx.Err() != nil {
		t.Fatalf("running %s %v did not finish within %s\nstdout:\n%s\nstderr:\n%s",
			name, args, bound, res.stdout, res.stderr)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running %s %v: %v", name, args, err)
	}
	res.exitCode = exitErr.ExitCode()
	return res
}
