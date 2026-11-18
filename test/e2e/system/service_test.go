//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// systemUnitPath is where service install --system writes the unit.
	systemUnitPath = "/etc/systemd/system/agent-peer.service"
	// systemConfigPath is the system Broker's configuration file.
	systemConfigPath = "/etc/agent-peer/config.yaml"
	// releaseBinary is the release build the base image installs.
	releaseBinary = "/usr/local/bin/agent-peer"
)

// TestServiceInstall checks the system-level installation inside a container
// whose process 1 is systemd: the unit file and the configuration file it
// writes, the enablement systemd records, and the path checks that guard the
// binary. The subtests share one container; install is idempotent, so each one
// starts from whatever the previous left and installs again when it needs to.
func TestServiceInstall(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Systemd)
		return
	}

	t.Run("system unit is written and enabled", func(t *testing.T) {
		res := installSystemService(t)
		if !strings.Contains(res.Stdout, "systemctl start agent-peer.service") {
			t.Errorf("service install --system did not print the start command:\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
		}

		content := readFile(t, systemUnitPath)
		for _, want := range []string{
			"ExecStart=" + releaseBinary + " broker --system",
			"Restart=always",
			"RestartSec=5",
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

		enabled := runCommandAs(t, "root", "systemctl", "is-enabled", "agent-peer.service")
		if state := strings.TrimSpace(enabled.Stdout); state != "enabled" {
			t.Errorf("systemctl is-enabled agent-peer.service = %q (exit %d), want %q\nstderr: %s",
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
		for i, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "#") {
				t.Errorf("line %d is not a comment: %q", i+1, line)
			}
		}
		if !strings.Contains(content, "agent-peer system Broker configuration") {
			t.Errorf("configuration file does not identify itself:\n%s", content)
		}

		uid, gid, mode := fileOwnerAndMode(t, systemConfigPath)
		if uid != 0 || gid != 0 {
			t.Errorf("configuration file owner = %d:%d, want 0:0", uid, gid)
		}
		if mode != 0o600 {
			t.Errorf("configuration file mode = %#o, want 0600", mode)
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
		res := runCommandAs(t, "root", releaseBinary, "service", "install")
		if res.ExitCode == 0 {
			t.Fatalf("service install as root without --system succeeded; stdout: %s, stderr: %s", res.Stdout, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "must not run as root") {
			t.Errorf("service install as root = exit %d, stderr %q, want a root rejection",
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
		dst := filepath.Join(t.TempDir(), "agent-peer")
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
		dst := filepath.Join(dir, "agent-peer")
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
		dst := filepath.Join(dir, "agent-peer")
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

// installSystemService runs service install --system as root and fails the
// subtest when it does not succeed.
func installSystemService(t *testing.T) syscontainer.Result {
	t.Helper()
	res := syscontainer.RunAs(t, "root", nil, "service", "install", "--system")
	if res.ExitCode != 0 {
		t.Fatalf("agent-peer service install --system: exit %d\nstdout: %s\nstderr: %s",
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

// copyBinary copies the release binary to dst with mode 0755.
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
	dir, err := os.MkdirTemp("/var/lib", "agent-peer-e2e-")
	if err != nil {
		t.Fatalf("creating a directory under /var/lib: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	return dir
}
