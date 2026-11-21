//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/wangzw/peers/test/e2e/syscontainer"
)

const (
	// systemUnitPath is where service install --system writes the unit.
	systemUnitPath = "/etc/systemd/system/peers.service"
	// systemConfigPath is the system Broker's configuration file.
	systemConfigPath = "/etc/peers/config.yaml"
	// systemSocket is the system Broker's local socket.
	systemSocket = "/run/peers/broker.sock"
	// releaseBinary is the peers build the base image installs.
	releaseBinary = "/usr/local/bin/peers"
	// systemAdminGroup is the default group a system install delegates
	// administration to.
	systemAdminGroup = "peers"
)

// TestServiceInstall checks the system installation inside a container
// whose process 1 is systemd: the unit file and the configuration file it
// writes, the enablement systemd records, and the path checks that guard the
// binary. The subtests share one container; install is idempotent, so each one
// starts from whatever the previous left and installs again when it needs to.
func TestServiceInstall(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	t.Run("root without a level flag installs the system installation", func(t *testing.T) {
		// Neither --system nor --user: root selects the system installation, and both
		// the summary and the written unit must show it.
		res := runCommandAs(t, "root", releaseBinary, "service", "install")
		if res.ExitCode != 0 {
			t.Fatalf("service install as root: exit %d\nstdout: %s\nstderr: %s",
				res.ExitCode, res.Stdout, res.Stderr)
		}
		if want := "installed (system service, starts at boot)\n"; !strings.Contains(res.Stdout, want) {
			t.Errorf("summary does not report the system installation:\n%s", res.Stdout)
		}
		if content := readFile(t, systemUnitPath); !strings.Contains(content,
			"ExecStart="+releaseBinary+" service run --system --allow-degraded") {
			t.Errorf("the installed unit is not the system one:\n%s", content)
		}
	})

	t.Run("system unit is written and enabled", func(t *testing.T) {
		installSystemService(t)

		content := readFile(t, systemUnitPath)
		for _, want := range []string{
			"ExecStart=" + releaseBinary + " service run --system --allow-degraded",
			"Restart=always",
			"RestartSec=5",
			"NoNewPrivileges=yes",
			"WantedBy=multi-user.target",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("unit file does not contain %q:\n%s", want, content)
			}
		}
		// The Broker reaches every served account's home, temporary directory
		// and runtime directory through helpers; sandboxing would break it. The
		// unit's own comment names the directives it must not set, so only
		// settings outside comments count.
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			for _, directive := range []string{"ProtectHome=", "PrivateTmp=", "ProtectSystem="} {
				if strings.HasPrefix(line, directive) {
					t.Errorf("unit file sets %q, which isolates paths the helpers need:\n%s", line, content)
				}
			}
		}

		uid, _, mode := fileOwnerAndMode(t, systemUnitPath)
		if uid != 0 {
			t.Errorf("unit file owner = %d, want 0", uid)
		}
		if mode != 0o644 {
			t.Errorf("unit file mode = %#o, want 0644", mode)
		}

		enabled := runCommandAs(t, "root", "systemctl", "is-enabled", "peers.service")
		if state := strings.TrimSpace(enabled.Stdout); state != "enabled" {
			t.Errorf("systemctl is-enabled peers.service = %q (exit %d), want %q\nstderr: %s",
				state, enabled.ExitCode, "enabled", enabled.Stderr)
		}

		verify := runCommandAs(t, "root", "systemd-analyze", "verify", systemUnitPath)
		if verify.ExitCode != 0 || verify.Stdout != "" || verify.Stderr != "" {
			t.Errorf("systemd-analyze verify %s: exit %d\nstdout: %s\nstderr: %s",
				systemUnitPath, verify.ExitCode, verify.Stdout, verify.Stderr)
		}
	})

	t.Run("system configuration template is created", func(t *testing.T) {
		installSystemService(t)

		content := readFile(t, systemConfigPath)
		if strings.TrimSpace(content) == "" {
			t.Fatalf("%s is empty", systemConfigPath)
		}
		if !strings.Contains(content, "peers system Broker configuration") {
			t.Errorf("configuration file does not identify itself:\n%s", content)
		}

		uid, gid, mode := fileOwnerAndMode(t, systemConfigPath)
		if uid != 0 {
			t.Errorf("configuration file owner = %d, want 0", uid)
		}
		adminGroup, err := user.LookupGroup(systemAdminGroup)
		if err != nil {
			t.Fatalf("looking up group %s: %v", systemAdminGroup, err)
		}
		wantGID, err := strconv.ParseUint(adminGroup.Gid, 10, 32)
		if err != nil {
			t.Fatalf("parsing gid %q of group %s: %v", adminGroup.Gid, systemAdminGroup, err)
		}
		if uint64(gid) != wantGID {
			t.Errorf("configuration file group = %d, want %s (%d)", gid, systemAdminGroup, wantGID)
		}
		if mode != 0o660 {
			t.Errorf("configuration file mode = %#o, want 0660", mode)
		}
	})

	t.Run("existing system configuration is kept", func(t *testing.T) {
		const existing = "broker:\n  alias: kept\n"
		if err := os.MkdirAll(filepath.Dir(systemConfigPath), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(systemConfigPath), err)
		}
		if err := os.WriteFile(systemConfigPath, []byte(existing), 0o600); err != nil {
			t.Fatalf("writing %s: %v", systemConfigPath, err)
		}
		if err := os.Chmod(systemConfigPath, 0o600); err != nil {
			t.Fatalf("chmod %s: %v", systemConfigPath, err)
		}
		if err := os.Chown(systemConfigPath, 0, 0); err != nil {
			t.Fatalf("chown %s: %v", systemConfigPath, err)
		}

		installSystemService(t)

		if got := readFile(t, systemConfigPath); got != existing {
			t.Errorf("service install --system overwrote the existing configuration:\n got: %q\nwant: %q", got, existing)
		}
	})

	t.Run("user install as root is refused", func(t *testing.T) {
		// Without a level flag root installs the system installation, so the
		// refused request is the explicit --user one.
		res := runCommandAs(t, "root", releaseBinary, "service", "install", "--user")
		if res.ExitCode == 0 {
			t.Fatalf("service install --user as root succeeded; stdout: %s, stderr: %s", res.Stdout, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "must not run as root") {
			t.Errorf("service install --user as root = exit %d, stderr %q, want a root rejection",
				res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	})

	t.Run("--system requires root", func(t *testing.T) {
		res := syscontainer.RunAs(t, "other", nil, "service", "install", "--system")
		if res.ExitCode == 0 {
			t.Fatalf("service install --system as other succeeded; stdout: %s, stderr: %s", res.Stdout, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "must run as root") {
			t.Errorf("service install --system as other = exit %d, stderr %q, want it to say it must run as root",
				res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	})

	t.Run("--system refuses a binary in the temporary directory", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "peers")
		copyBinary(t, dst)

		res := runCommandAs(t, "root", dst, "service", "install", "--system")
		if res.ExitCode == 0 {
			t.Fatalf("service install --system accepted a binary under %s", os.TempDir())
		}
		if !strings.Contains(res.Stderr, "temporary directory") {
			t.Errorf("refusing a temporary-directory binary = exit %d, stderr %q, want a temporary-directory reason",
				res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	})

	t.Run("--system refuses a binary another account owns", func(t *testing.T) {
		dir := tempDirOutsideTmp(t)
		dst := filepath.Join(dir, "peers")
		copyBinary(t, dst)
		if err := os.Chown(dst, 2000, 2000); err != nil {
			t.Fatalf("chown %s to other: %v", dst, err)
		}

		res := runCommandAs(t, "root", dst, "service", "install", "--system")
		if res.ExitCode == 0 {
			t.Fatalf("service install --system accepted a binary owned by another account")
		}
		if !strings.Contains(res.Stderr, "not root") {
			t.Errorf("refusing a non-root binary = exit %d, stderr %q, want it to name the owner",
				res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	})

	t.Run("--system refuses a group-writable binary", func(t *testing.T) {
		dir := tempDirOutsideTmp(t)
		dst := filepath.Join(dir, "peers")
		copyBinary(t, dst)
		if err := os.Chmod(dst, 0o775); err != nil {
			t.Fatalf("chmod %s: %v", dst, err)
		}

		res := runCommandAs(t, "root", dst, "service", "install", "--system")
		if res.ExitCode == 0 {
			t.Fatalf("service install --system accepted a group-writable binary")
		}
		if !strings.Contains(res.Stderr, "writable by group or others") {
			t.Errorf("refusing a group-writable binary = exit %d, stderr %q, want a writability reason",
				res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	})
}

// TestServiceUnitsVerify installs the user unit through the product's
// subcommand and checks that systemd records it as enabled and accepts it
// without a warning.
func TestServiceUnitsVerify(t *testing.T) {
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

	// enable and verify both ask the account's user manager, so start it the
	// way a login would and wait for its bus.
	mustRun(t, "root", nil, "loginctl", "enable-linger", "peer")
	mustRun(t, "root", nil, "systemctl", "start", "user@"+uid+".service")
	waitUserManager(t, peerUID)

	install := runWithEnv(t, "peer", userEnv, "peers", "service", "install")
	if install.ExitCode != 0 {
		t.Fatalf("peers service install as peer: exit %d\nstdout: %s\nstderr: %s",
			install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
	}

	assertUserUnitEnabled(t, userEnv)
	assertUserUnitVerifies(t, userEnv)
}

// TestSystemServiceUninstall stops the running system Broker, removes the unit
// file and keeps the configuration. A second run reports that nothing is
// installed and still succeeds.
func TestSystemServiceUninstall(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	const configYAML = "broker:\n  alias: kept\n"
	if err := os.MkdirAll(filepath.Dir(systemConfigPath), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(systemConfigPath), err)
	}
	if err := os.WriteFile(systemConfigPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("writing %s: %v", systemConfigPath, err)
	}
	if err := os.Chmod(systemConfigPath, 0o600); err != nil {
		t.Fatalf("chmod %s: %v", systemConfigPath, err)
	}
	if err := os.Chown(systemConfigPath, 0, 0); err != nil {
		t.Fatalf("chown %s: %v", systemConfigPath, err)
	}

	install := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if install.ExitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s",
			install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
	}
	syscontainer.WaitFor(t, "the system Broker socket "+systemSocket, func() bool {
		_, err := os.Stat(systemSocket)
		return err == nil
	})

	res := syscontainer.RunAs(t, "root", nil, "service", "remove", "--system")
	if res.ExitCode != 0 {
		t.Fatalf("service remove --system: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}

	active := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	if state := strings.TrimSpace(active.Stdout); state != "inactive" && state != "failed" {
		t.Errorf("systemctl is-active peers.service = %q after uninstall, want inactive or failed", state)
	}
	if procs := syscontainer.Pids("peers", "service", "run", "--system"); len(procs) != 0 {
		t.Errorf("system Broker still runs after uninstall: %v", procs)
	}
	if _, err := os.Stat(systemUnitPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit file still exists after uninstall: %v", err)
	}
	if got := readFile(t, systemConfigPath); got != configYAML {
		t.Errorf("configuration after uninstall = %q, want %q", got, configYAML)
	}

	again := syscontainer.RunAs(t, "root", nil, "service", "remove", "--system")
	if again.ExitCode != 0 {
		t.Fatalf("second service remove --system: exit %d\nstdout: %s\nstderr: %s",
			again.ExitCode, strings.TrimSpace(again.Stdout), strings.TrimSpace(again.Stderr))
	}
	if !strings.Contains(again.Stdout, "not installed") {
		t.Errorf("second uninstall stdout = %q, want it to say nothing is installed", again.Stdout)
	}
}

// TestSystemServiceUninstallAfterUnitFileRemoved reproduces the state a
// deleted unit file leaves behind: systemd still has the unit loaded and
// running, but the file is gone. Uninstall must stop the Broker anyway.
func TestSystemServiceUninstallAfterUnitFileRemoved(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	t.Cleanup(func() {
		_ = runCommandAs(t, "root", "systemctl", "stop", "peers.service")
		_ = runCommandAs(t, "root", "systemctl", "disable", "peers.service")
		_ = runCommandAs(t, "root", "systemctl", "daemon-reload")
		_ = os.Remove(systemUnitPath)
	})

	install := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if install.ExitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s",
			install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
	}
	syscontainer.WaitFor(t, "the system Broker socket "+systemSocket, func() bool {
		_, err := os.Stat(systemSocket)
		return err == nil
	})

	enabledBefore := runCommandAs(t, "root", "systemctl", "is-enabled", "peers.service")
	activeBefore := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	t.Logf("before removing the unit file: is-enabled=%q (exit %d) is-active=%q (exit %d)",
		strings.TrimSpace(enabledBefore.Stdout), enabledBefore.ExitCode,
		strings.TrimSpace(activeBefore.Stdout), activeBefore.ExitCode)

	// Remove the unit file without telling systemd. The loaded unit keeps
	// running and the enable symlink still exists.
	if err := os.Remove(systemUnitPath); err != nil {
		t.Fatalf("removing %s: %v", systemUnitPath, err)
	}
	enabledAfter := runCommandAs(t, "root", "systemctl", "is-enabled", "peers.service")
	activeAfter := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	t.Logf("after removing the unit file, before uninstall: is-enabled=%q (exit %d) is-active=%q (exit %d)",
		strings.TrimSpace(enabledAfter.Stdout), enabledAfter.ExitCode,
		strings.TrimSpace(activeAfter.Stdout), activeAfter.ExitCode)

	if procs := syscontainer.Pids("peers", "service", "run", "--system"); len(procs) == 0 {
		t.Fatal("system Broker is not running after the unit file was removed; uninstall would have nothing to stop")
	}

	res := syscontainer.RunAs(t, "root", nil, "service", "remove", "--system")
	t.Logf("uninstall: exit=%d stdout=%q stderr=%q", res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	if res.ExitCode != 0 {
		t.Fatalf("service remove --system: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}

	procs := syscontainer.Pids("peers", "service", "run", "--system")
	if len(procs) != 0 {
		t.Errorf("system Broker still runs after uninstall: %v", procs)
	}
	active := runCommandAs(t, "root", "systemctl", "is-active", "peers.service")
	state := strings.TrimSpace(active.Stdout)
	t.Logf("after uninstall: is-active=%q (exit %d)", state, active.ExitCode)
	if state == "active" || state == "deactivating" {
		t.Errorf("systemctl is-active peers.service = %q after uninstall, want it stopped", state)
	}
}

// TestUserServiceUninstall stops the running user Broker, removes the unit
// file and keeps the account's configuration.
func TestUserServiceUninstall(t *testing.T) {
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
	socket := "/tmp/peers-" + uid + "/broker.sock"
	const configYAML = "broker:\n  listen: \":17445\"\n"
	writeAccountFile(t, "peer", "/home/peer/.peers/config.yaml", configYAML)

	mustRun(t, "root", nil, "loginctl", "enable-linger", "peer")
	mustRun(t, "root", nil, "systemctl", "start", "user@"+uid+".service")
	waitUserManager(t, peerUID)

	install := runWithEnv(t, "peer", userEnv, releaseBinary, "service", "install")
	if install.ExitCode != 0 {
		t.Fatalf("service install as peer: exit %d\nstdout: %s\nstderr: %s",
			install.ExitCode, strings.TrimSpace(install.Stdout), strings.TrimSpace(install.Stderr))
	}
	syscontainer.WaitFor(t, "the peer Broker socket "+socket, func() bool {
		_, err := os.Stat(socket)
		return err == nil
	})

	res := runWithEnv(t, "peer", userEnv, releaseBinary, "service", "remove")
	if res.ExitCode != 0 {
		t.Fatalf("service remove as peer: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}

	active := runWithEnv(t, "peer", userEnv, "systemctl", "--user", "is-active", "peers.service")
	if state := strings.TrimSpace(active.Stdout); state != "inactive" && state != "failed" {
		t.Errorf("systemctl --user is-active peers.service = %q after uninstall, want inactive or failed", state)
	}
	if procs := peerBrokers(peerUID); len(procs) != 0 {
		t.Errorf("peer Broker still runs after uninstall: %v", procs)
	}
	if _, err := os.Stat(userUnitPath(t)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("user unit file still exists after uninstall: %v", err)
	}
	if got := readFile(t, "/home/peer/.peers/config.yaml"); got != configYAML {
		t.Errorf("configuration after uninstall = %q, want %q", got, configYAML)
	}

	again := runWithEnv(t, "peer", userEnv, releaseBinary, "service", "remove")
	if again.ExitCode != 0 {
		t.Fatalf("second service remove as peer: exit %d\nstdout: %s\nstderr: %s",
			again.ExitCode, strings.TrimSpace(again.Stdout), strings.TrimSpace(again.Stderr))
	}
	if !strings.Contains(again.Stdout, "not installed") {
		t.Errorf("second uninstall stdout = %q, want it to say nothing is installed", again.Stdout)
	}
}

// installSystemService runs service install --system as root and fails the
// subtest when it does not succeed.
func installSystemService(t *testing.T) syscontainer.Result {
	t.Helper()
	res := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if res.ExitCode != 0 {
		t.Fatalf("peers service install --system: exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr))
	}
	return res
}

// runCommandAs runs name with args as account and returns its output and exit
// code. A process that cannot be started fails the test.
func runCommandAs(t *testing.T, account, name string, args ...string) syscontainer.Result {
	t.Helper()
	cmd := syscontainer.Command(account, nil, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	result := syscontainer.Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running %s as %s: %v", name, account, err)
	}
	result.ExitCode = exitErr.ExitCode()
	return result
}

// readFile returns the content of path or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// fileOwnerAndMode returns the owner, group and permission bits of path.
func fileOwnerAndMode(t *testing.T, path string) (uid, gid uint32, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s has no owner information", path)
	}
	return st.Uid, st.Gid, info.Mode().Perm()
}

// copyBinary copies the image's peers binary to dst with mode 0755.
func copyBinary(t *testing.T, dst string) {
	t.Helper()
	data, err := os.ReadFile(releaseBinary)
	if err != nil {
		t.Fatalf("reading %s: %v", releaseBinary, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", dst, err)
	}
}

// tempDirOutsideTmp creates a root-owned directory outside the temporary
// directory, so the temporary-directory check cannot mask the path check under
// test. The directory is removed when the test ends.
func tempDirOutsideTmp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/lib", "peers-e2e-")
	if err != nil {
		t.Fatalf("creating a directory under /var/lib: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	return dir
}
