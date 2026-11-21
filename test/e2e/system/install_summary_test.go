//go:build e2e

package e2e

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/wangzw/peers/test/e2e/syscontainer"
)

// systemAuditPath is where the system Broker writes its audit log.
const systemAuditPath = "/var/log/peers/audit.log"

// TestREQBrokerConfig050_SystemServiceInstallPrintsSummary covers the summary
// a system-level installation prints: the level, the binary, the configuration
// file and the log locations, the commands that control the service and the
// uninstall command. The paths it prints must exist on the host. A second
// installation over the kept configuration says (kept) instead of (generated).
func TestREQBrokerConfig050_SystemServiceInstallPrintsSummary(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	resetSystemServiceState(t)
	// The printed uninstall removes the binary, so a later reset would have no
	// executable to run. The uninstall itself already removed the unit.
	t.Cleanup(func() {
		if _, err := os.Stat(releaseBinary); err == nil {
			resetSystemServiceState(t)
		}
	})

	res := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if res.ExitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
	assertInstallSummary(t, res.Stdout, "system-level", "boot", map[string]string{
		"binary:":    releaseBinary,
		"config:":    systemConfigPath + " (generated)",
		"run log:":   "journalctl -u peers.service",
		"audit log:": systemAuditPath,
		"start:":     "sudo peers service start",
		"stop:":      "sudo peers service stop",
		"restart:":   "sudo peers service restart",
		"uninstall:": "peers uninstall",
	})

	syscontainer.WaitFor(t, "the system Broker socket "+systemSocket, func() bool {
		_, err := os.Stat(systemSocket)
		return err == nil
	})
	for _, path := range []string{releaseBinary, systemConfigPath, systemAuditPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the summary prints %s, but it does not exist: %v", path, err)
		}
	}

	// A second installation keeps the configuration file and says so.
	again := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if again.ExitCode != 0 {
		t.Fatalf("reinstalling service --system: exit %d\nstdout: %s\nstderr: %s",
			again.ExitCode, strings.TrimSpace(again.Stdout), strings.TrimSpace(again.Stderr))
	}
	assertInstallSummary(t, again.Stdout, "system-level", "boot", map[string]string{
		"config:": systemConfigPath + " (kept)",
	})

	// The commands the summary printed must run as printed, in order. The test
	// account is root, so the printed sudo prefix is dropped and the uninstall
	// runs as root directly. The path where a non-root account answers the
	// system-uninstall question and the command re-executes itself through sudo
	// is covered by the uninstall command tests (TestUninstallAccountOnlySystem*).
	t.Run("the printed commands run as printed", func(t *testing.T) {
		for _, label := range []string{"start:", "stop:", "restart:", "uninstall:"} {
			args := printedCommandArgs(t, summaryValue(t, again.Stdout, label))
			res := syscontainer.RunAs(t, "root", nil, args...)
			if res.ExitCode != 0 {
				t.Fatalf("%s command from the summary: exit %d\nstdout: %s\nstderr: %s",
					strings.TrimSuffix(label, ":"), res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
			}
		}
	})
}

// TestREQBrokerConfig050_UserServiceInstallPrintsSummary covers the same
// summary for a user-level installation: no sudo prefix, starts at login, the
// user's configuration file and log commands.
func TestREQBrokerConfig050_UserServiceInstallPrintsSummary(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	peerUID := syscontainer.AccountUID(t, "peer")
	uid := strconv.FormatUint(uint64(peerUID), 10)
	userEnv := []string{
		"XDG_RUNTIME_DIR=/run/user/" + uid,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + uid + "/bus",
	}
	configPath := "/home/peer/.peers/config.yaml"
	auditPath := "/home/peer/.peers/audit.log"
	socket := "/tmp/peers-" + uid + "/broker.sock"

	mustRun(t, "root", nil, "loginctl", "enable-linger", "peer")
	mustRun(t, "root", nil, "systemctl", "start", "user@"+uid+".service")
	waitUserManager(t, peerUID)
	t.Cleanup(func() {
		_ = runWithEnv(t, "peer", userEnv, releaseBinary, "service", "uninstall")
	})

	res := runWithEnv(t, "peer", userEnv, releaseBinary, "service", "install")
	if res.ExitCode != 0 {
		t.Fatalf("service install as peer: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
	assertInstallSummary(t, res.Stdout, "user-level", "login", map[string]string{
		"binary:":    releaseBinary,
		"config:":    configPath + " (generated)",
		"run log:":   "journalctl --user -u peers.service",
		"audit log:": auditPath,
		"start:":     "peers service start",
		"stop:":      "peers service stop",
		"restart:":   "peers service restart",
		"uninstall:": "peers uninstall",
	})

	syscontainer.WaitFor(t, "the peer Broker socket "+socket, func() bool {
		_, err := os.Stat(socket)
		return err == nil
	})
	for _, path := range []string{releaseBinary, configPath, auditPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the summary prints %s, but it does not exist: %v", path, err)
		}
	}

	again := runWithEnv(t, "peer", userEnv, releaseBinary, "service", "install")
	if again.ExitCode != 0 {
		t.Fatalf("reinstalling the user service: exit %d\nstdout: %s\nstderr: %s",
			again.ExitCode, strings.TrimSpace(again.Stdout), strings.TrimSpace(again.Stderr))
	}
	assertInstallSummary(t, again.Stdout, "user-level", "login", map[string]string{
		"config:": configPath + " (kept)",
	})

	// The commands the summary printed must run as printed, in order. The test
	// runs them as the account that installed the service, which is what the
	// summary addresses; the printed form has no sudo prefix at this level.
	t.Run("the printed commands run as printed", func(t *testing.T) {
		for _, label := range []string{"start:", "stop:", "restart:", "uninstall:"} {
			args := printedCommandArgs(t, summaryValue(t, again.Stdout, label))
			res := runWithEnv(t, "peer", userEnv, "peers", args...)
			if res.ExitCode != 0 {
				t.Fatalf("%s command from the summary: exit %d\nstdout: %s\nstderr: %s",
					strings.TrimSuffix(label, ":"), res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
			}
		}
	})
}

// TestREQBrokerConfig064_ReinstallReportsDegradedAuditLog covers the first
// half of REQ-broker-config-064 on a container: after a successful system
// installation, making the audit log unusable and reinstalling must keep the
// installed, running service, name the fault and the repair command, exit 3,
// print the summary, and leave a Broker that peers status reports as
// degraded. The second half, a listen port that is occupied before the
// installation, is covered by TestSystemServiceInstallListenPortConflict.
func TestREQBrokerConfig064_ReinstallReportsDegradedAuditLog(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	resetSystemServiceState(t)
	t.Cleanup(func() {
		_ = os.Remove(systemAuditPath)
		resetSystemServiceState(t)
	})

	installSystemService(t)
	syscontainer.WaitFor(t, "the system Broker socket "+systemSocket, func() bool {
		_, err := os.Stat(systemSocket)
		return err == nil
	})

	// A directory at the audit log path cannot be opened for appending even by
	// root, so the Broker's audit step fails the same way a directory the
	// service may not write to would.
	if err := os.Remove(systemAuditPath); err != nil {
		t.Fatalf("removing %s: %v", systemAuditPath, err)
	}
	if err := os.Mkdir(systemAuditPath, 0o755); err != nil {
		t.Fatalf("replacing %s with a directory: %v", systemAuditPath, err)
	}

	res := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if res.ExitCode != 3 {
		t.Fatalf("reinstalling with an unusable audit log: exit %d, want 3\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}

	// The summary is printed as usual, and the service is still installed.
	assertInstallSummary(t, res.Stdout, "system-level", "boot", map[string]string{
		"binary:":    releaseBinary,
		"config:":    systemConfigPath + " (kept)",
		"audit log:": systemAuditPath,
	})
	if _, err := os.Stat(systemUnitPath); err != nil {
		t.Errorf("the unit file %s is missing after the degraded reinstall: %v", systemUnitPath, err)
	}
	if _, err := os.Stat(releaseBinary); err != nil {
		t.Errorf("the binary %s is missing after the degraded reinstall: %v", releaseBinary, err)
	}

	// The two degraded lines name the level, the reason and the repair command.
	degradedLine := summaryValue(t, res.Stderr, "Broker:")
	if !strings.Contains(degradedLine, "degraded (diagnostics only)") {
		t.Errorf("degraded line = %q, want the diagnostics-only level", degradedLine)
	}
	if !strings.Contains(degradedLine, "audit") {
		t.Errorf("degraded line = %q, want the audit log reason", degradedLine)
	}
	fixLine := summaryValue(t, res.Stderr, "Fix:")
	if !strings.Contains(fixLine, systemAuditPath) {
		t.Errorf("fix line = %q, want the audit log path %s", fixLine, systemAuditPath)
	}
	if !strings.Contains(fixLine, "peers service restart") {
		t.Errorf("fix line = %q, want the service restart command", fixLine)
	}

	// The service is still running, and status reports it as degraded.
	active := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	if state := strings.TrimSpace(active.Stdout); state != "active" {
		t.Errorf("systemctl is-active peers.service = %q after the degraded reinstall, want %q\nstderr: %s",
			state, "active", strings.TrimSpace(active.Stderr))
	}
	status := syscontainer.RunAs(t, "root", nil, "status")
	if status.ExitCode == 0 {
		t.Errorf("peers status exited 0 for a diagnostics-only Broker\nstdout: %s\nstderr: %s",
			strings.TrimSpace(status.Stdout), strings.TrimSpace(status.Stderr))
	}
	if combined := status.Stdout + status.Stderr; !strings.Contains(combined, "degraded") {
		t.Errorf("peers status output = %q, want it to report the degraded run", combined)
	}
}

// assertInstallSummary checks the first line of an install summary and every
// value the caller names. level and start are the words the summary uses for
// the install level and when the service starts.
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
// arguments that follow its binary name. It drops the sudo prefix a system-level
// summary prints, because the caller runs the command as root directly.
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

// summaryValue returns the value of the summary line that starts with label, or
// fails the test when the line is absent.
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
