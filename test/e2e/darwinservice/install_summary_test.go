//go:build e2e_darwin

package darwinservice

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// systemAuditPath is where the system Broker writes its audit log.
const systemAuditPath = "/var/log/peers/audit.log"

// userConfigPath returns the running account's configuration file.
func userConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".peers", "config.yaml")
}

// userAuditPath returns the running account's audit log.
func userAuditPath() string {
	return filepath.Join(os.Getenv("HOME"), ".peers", "audit.log")
}

// userRunLogPath returns the log launchd writes for the user-level Broker.
func userRunLogPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Logs", "peers", "broker.log")
}

// TestREQBrokerConfig050_UserServiceInstallPrintsSummary covers the summary a
// user-level installation prints on macOS: no sudo prefix, starts at login, the
// account's configuration file and log locations, the control commands and the
// uninstall command. The paths it prints must exist. A second installation over
// the kept configuration says (kept).
func TestREQBrokerConfig050_UserServiceInstallPrintsSummary(t *testing.T) {
	resetUserService(t)
	t.Cleanup(func() { resetUserService(t) })

	// Start from no configuration file so the first installation generates
	// one; preserveUserConfig restores the account's file when the test ends.
	configPath := preserveUserConfig(t)
	if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removing %s: %v", configPath, err)
	}

	res := run(t, installedBinary, "service", "install")
	if res.exitCode != 0 {
		t.Fatalf("service install: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	assertInstallSummary(t, res.stdout, "user-level", "login", map[string]string{
		"binary:":    installedBinary,
		"config:":    configPath + " (generated)",
		"run log:":   userRunLogPath(),
		"audit log:": userAuditPath(),
		"start:":     "peers service start",
		"stop:":      "peers service stop",
		"restart:":   "peers service restart",
		"uninstall:": "peers uninstall",
	})

	waitForFile(t, userAuditPath(), serviceStartBound)
	waitForFile(t, userRunLogPath(), serviceStartBound)
	for _, path := range []string{installedBinary, configPath, userAuditPath()} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the summary prints %s, but it does not exist: %v", path, err)
		}
	}

	again := run(t, installedBinary, "service", "install")
	if again.exitCode != 0 {
		t.Fatalf("reinstalling the user service: exit %d\nstdout: %s\nstderr: %s", again.exitCode, again.stdout, again.stderr)
	}
	assertInstallSummary(t, again.stdout, "user-level", "login", map[string]string{
		"config:": configPath + " (kept)",
	})

	// The commands the summary printed must run as printed, in order, as the
	// account that installed the service. The binary name resolves to the test
	// copy because it is not installed under the printed name.
	t.Run("the printed commands run as printed", func(t *testing.T) {
		for _, label := range []string{"start:", "stop:", "restart:", "uninstall:"} {
			args := printedCommandArgs(t, summaryValue(t, again.stdout, label))
			res := run(t, installedBinary, args...)
			if res.exitCode != 0 {
				t.Fatalf("%s command from the summary: exit %d\nstdout: %s\nstderr: %s",
					strings.TrimSuffix(label, ":"), res.exitCode, res.stdout, res.stderr)
			}
		}
	})
}

// TestREQBrokerConfig050_SystemServiceInstallPrintsSummary covers the same
// summary for a system-level installation: the sudo prefix, starts at boot, and
// the system paths.
func TestREQBrokerConfig050_SystemServiceInstallPrintsSummary(t *testing.T) {
	resetSystemService(t)
	t.Cleanup(func() { resetSystemService(t) })
	preserveSystemFile(t, systemConfigPath)

	// Start from no configuration file so the first installation generates
	// one; preserveSystemFile restores a pre-existing file when the test ends.
	if err := sudo("rm", "-f", systemConfigPath); err != nil {
		t.Fatalf("removing %s: %v", systemConfigPath, err)
	}

	res := runSudo(t, installedBinary, "service", "install", "--system")
	if res.exitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s", res.exitCode, res.stdout, res.stderr)
	}
	assertInstallSummary(t, res.stdout, "system-level", "boot", map[string]string{
		"binary:":    installedBinary,
		"config:":    systemConfigPath + " (generated)",
		"run log:":   "/var/log/peers/broker.log",
		"audit log:": systemAuditPath,
		"start:":     "sudo peers service start",
		"stop:":      "sudo peers service stop",
		"restart:":   "sudo peers service restart",
		"uninstall:": "peers uninstall",
	})

	waitForRootFile(t, systemAuditPath, serviceStartBound)
	waitForRootFile(t, "/var/log/peers/broker.log", serviceStartBound)
	for _, path := range []string{installedBinary, systemConfigPath, systemAuditPath} {
		if err := sudo("test", "-e", path); err != nil {
			t.Errorf("the summary prints %s, but it does not exist: %v", path, err)
		}
	}

	again := runSudo(t, installedBinary, "service", "install", "--system")
	if again.exitCode != 0 {
		t.Fatalf("reinstalling the system service: exit %d\nstdout: %s\nstderr: %s", again.exitCode, again.stdout, again.stderr)
	}
	assertInstallSummary(t, again.stdout, "system-level", "boot", map[string]string{
		"config:": systemConfigPath + " (kept)",
	})

	// The commands the summary printed must run as printed, in order. The
	// printed sudo prefix is applied through runSudo, and the binary name
	// resolves to the test copy because it is not installed under the printed
	// name.
	t.Run("the printed commands run as printed", func(t *testing.T) {
		for _, label := range []string{"start:", "stop:", "restart:", "uninstall:"} {
			args := printedCommandArgs(t, summaryValue(t, again.stdout, label))
			res := runSudo(t, installedBinary, args...)
			if res.exitCode != 0 {
				t.Fatalf("%s command from the summary: exit %d\nstdout: %s\nstderr: %s",
					strings.TrimSuffix(label, ":"), res.exitCode, res.stdout, res.stderr)
			}
		}
	})
}

// TestREQBrokerConfig064_ReinstallReportsDegradedAuditLog covers the first half
// of REQ-broker-config-064 on macOS, at both levels: after a successful
// installation, making the audit log unusable and reinstalling must keep the
// installed, running service, name the fault and the repair command, exit 3,
// print the summary, and leave a Broker that peers service status reports as
// degraded. The listen-port half is covered by
// TestUserServiceInstallRefusesAnOccupiedListenPort.
func TestREQBrokerConfig064_ReinstallReportsDegradedAuditLog(t *testing.T) {
	t.Run("user level", func(t *testing.T) {
		resetUserService(t)
		preserveUserAudit(t)
		t.Cleanup(func() { resetUserService(t) })

		writeUserConfig(t, "127.0.0.1:17449")
		install := run(t, installedBinary, "service", "install")
		if install.exitCode != 0 {
			t.Fatalf("service install: exit %d\nstdout: %s\nstderr: %s", install.exitCode, install.stdout, install.stderr)
		}
		domain := userDomain(t)
		beforePID := waitForPID(t, domain, serviceStartBound)

		breakAuditPath(t, userAuditPath(), false)

		res := run(t, installedBinary, "service", "install")
		if res.exitCode != 3 {
			t.Fatalf("reinstalling with an unusable audit log: exit %d, want 3\nstdout: %s\nstderr: %s",
				res.exitCode, res.stdout, res.stderr)
		}
		assertInstallSummary(t, res.stdout, "user-level", "login", map[string]string{
			"binary:":    installedBinary,
			"config:":    userConfigPath() + " (kept)",
			"audit log:": userAuditPath(),
		})
		assertDegradedOutput(t, res.stderr, userAuditPath())

		// The relaunched service still runs, with a new pid.
		afterPID := waitForPID(t, domain, serviceStartBound)
		if afterPID == 0 || !processAlive(afterPID) {
			t.Errorf("the user service did not survive the degraded reinstall; pid before %d, after %d", beforePID, afterPID)
		}

		status := run(t, installedBinary, "service", "status")
		if status.exitCode == 0 {
			t.Errorf("peers service status exited 0 for a diagnostics-only Broker\nstdout: %s\nstderr: %s", status.stdout, status.stderr)
		}
		if combined := status.stdout + status.stderr; !strings.Contains(combined, "degraded") {
			t.Errorf("peers service status output = %q, want it to report the degraded run", combined)
		}
	})

	t.Run("system level", func(t *testing.T) {
		resetSystemService(t)
		preserveSystemFile(t, systemAuditPath)
		t.Cleanup(func() { resetSystemService(t) })

		install := runSudo(t, installedBinary, "service", "install", "--system")
		if install.exitCode != 0 {
			t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s", install.exitCode, install.stdout, install.stderr)
		}
		beforePID := waitForPID(t, "system", serviceStartBound)

		breakAuditPath(t, systemAuditPath, true)

		res := runSudo(t, installedBinary, "service", "install", "--system")
		if res.exitCode != 3 {
			t.Fatalf("reinstalling the system service with an unusable audit log: exit %d, want 3\nstdout: %s\nstderr: %s",
				res.exitCode, res.stdout, res.stderr)
		}
		assertInstallSummary(t, res.stdout, "system-level", "boot", map[string]string{
			"binary:":    installedBinary,
			"audit log:": systemAuditPath,
		})
		assertDegradedOutput(t, res.stderr, systemAuditPath)

		afterPID := waitForPID(t, "system", serviceStartBound)
		if afterPID == 0 || !processAlive(afterPID) {
			t.Errorf("the system service did not survive the degraded reinstall; pid before %d, after %d", beforePID, afterPID)
		}

		status := runSudo(t, installedBinary, "service", "status")
		if status.exitCode == 0 {
			t.Errorf("peers service status as root exited 0 for a diagnostics-only Broker\nstdout: %s\nstderr: %s", status.stdout, status.stderr)
		}
		if combined := status.stdout + status.stderr; !strings.Contains(combined, "degraded") {
			t.Errorf("peers service status output = %q, want it to report the degraded run", combined)
		}
	})
}

// assertDegradedOutput checks the two degraded lines the command writes to
// standard error: the level and reason, then the fix that names auditPath.
func assertDegradedOutput(t *testing.T, stderr, auditPath string) {
	t.Helper()

	degradedLine := summaryValue(t, stderr, "Broker:")
	if !strings.Contains(degradedLine, "degraded (diagnostics only)") {
		t.Errorf("degraded line = %q, want the diagnostics-only level", degradedLine)
	}
	if !strings.Contains(degradedLine, "audit") {
		t.Errorf("degraded line = %q, want the audit log reason", degradedLine)
	}
	fixLine := summaryValue(t, stderr, "Fix:")
	if !strings.Contains(fixLine, auditPath) {
		t.Errorf("fix line = %q, want the audit log path %s", fixLine, auditPath)
	}
	if !strings.Contains(fixLine, "peers service restart") {
		t.Errorf("fix line = %q, want the service restart command", fixLine)
	}
}

// breakAuditPath replaces path with a directory, which cannot be opened for
// appending even by root. root selects the sudo path for a system-level file.
func breakAuditPath(t *testing.T, path string, root bool) {
	t.Helper()

	if root {
		if err := sudo("rm", "-f", path); err != nil {
			t.Fatalf("removing %s: %v", path, err)
		}
		if err := sudo("mkdir", "-m", "0755", path); err != nil {
			t.Fatalf("replacing %s with a directory: %v", path, err)
		}
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replacing %s with a directory: %v", path, err)
	}
}

// preserveUserAudit restores the account's audit log and removes the directory
// that breakAuditPath created, so a developer machine keeps its log.
func preserveUserAudit(t *testing.T) {
	t.Helper()

	path := userAuditPath()
	data, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("removing %s: %v", path, err)
		}
		if existed {
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Errorf("restoring %s: %v", path, err)
			}
		}
	})
}

// preserveSystemFile keeps a copy of path and restores it when the test ends.
// A later cleanup removes whatever the test left at path first.
func preserveSystemFile(t *testing.T, path string) {
	t.Helper()

	backup := path + ".e2e-backup"
	if err := sudo("cp", "-p", path, backup); err != nil {
		// The file did not exist; remove whatever the test creates.
		t.Cleanup(func() { _ = sudo("rm", "-rf", path) })
		return
	}
	t.Cleanup(func() {
		_ = sudo("rm", "-rf", path)
		if err := sudo("mv", backup, path); err != nil {
			t.Errorf("restoring %s: %v", path, err)
		}
	})
}

// assertInstallSummary checks the first line of an install summary and every
// value the caller names.
func assertInstallSummary(t *testing.T, stdout, level, start string, wants map[string]string) {
	t.Helper()

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("the install printed no summary:\n%s", stdout)
	}
	first := strings.TrimSpace(lines[0])
	suffix := " installed (" + level + ", starts at " + start + ")"
	if !strings.HasPrefix(first, "peers ") || !strings.HasSuffix(first, suffix) {
		t.Errorf("summary first line = %q, want %q", first, "peers <version>"+suffix)
	}
	if version := strings.TrimSuffix(strings.TrimPrefix(first, "peers "), suffix); strings.TrimSpace(version) == "" || strings.Contains(version, " ") {
		t.Errorf("summary first line = %q, want one version word", first)
	}

	for label, want := range wants {
		if got := summaryValue(t, stdout, label); got != want {
			t.Errorf("summary %s = %q, want %q", label, got, want)
		}
	}
}

// printedCommandArgs splits a command from the install summary into the
// arguments that follow its binary name. The caller runs the arguments with the
// installed test binary, which is the copy the printed peers names; the
// sudo prefix a system-level summary prints is dropped and applied by the
// caller.
func printedCommandArgs(t *testing.T, command string) []string {
	t.Helper()

	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "sudo" {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != "peers" {
		t.Fatalf("summary command = %q, want it to name peers", command)
	}
	return words[1:]
}

// summaryValue returns the value of the line that starts with label, or fails
// the test when the line is absent.
func summaryValue(t *testing.T, output, label string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, label) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, label))
		}
	}
	t.Fatalf("output has no %q line:\n%s", label, output)
	return ""
}

// waitForFile waits until path exists.
func waitForFile(t *testing.T, path string, bound time.Duration) {
	t.Helper()

	waitFor(t, bound, path, func() (bool, string) {
		_, err := os.Stat(path)
		return err == nil, errDetail(err)
	})
}

// waitForRootFile waits until a root-owned path exists.
func waitForRootFile(t *testing.T, path string, bound time.Duration) {
	t.Helper()

	waitFor(t, bound, path, func() (bool, string) {
		err := sudo("test", "-e", path)
		return err == nil, errDetail(err)
	})
}

// errDetail renders err for a wait's failure detail.
func errDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runSudo runs name with args through sudo and returns its output and exit
// code. A process that cannot be started fails the test.
func runSudo(t *testing.T, name string, args ...string) result {
	t.Helper()

	cmd := exec.Command("sudo", append([]string{"-n", name}, args...)...)
	out, err := cmd.Output()
	res := result{stdout: string(out)}
	if err == nil {
		return res
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running sudo %s %v: %v", name, args, err)
	}
	res.exitCode = exitErr.ExitCode()
	res.stderr = string(exitErr.Stderr)
	return res
}
