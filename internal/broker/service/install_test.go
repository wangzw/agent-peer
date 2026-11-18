package service

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// baseOptions returns user-level Linux options rooted in a temporary directory.
func baseOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		GOOS:     "linux",
		EUID:     1000,
		UID:      1000,
		Exe:      "/usr/local/bin/agent-peer",
		Home:     "/home/alice",
		TempDir:  "/tmp",
		Root:     t.TempDir(),
		LookPath: func(file string) (string, error) { return filepath.Join("/bin", file), nil },
		Run:      func(string, ...string) ([]byte, error) { return nil, nil },
		Owner:    fakeOwner(nil),
		EvalSymlinks: func(path string) (string, error) {
			return path, nil
		},
	}
}

type fakeOwnerEntry struct {
	uid  uint32
	mode fs.FileMode
}

func fakeOwner(overrides map[string]fakeOwnerEntry) func(string) (uint32, fs.FileMode, error) {
	return func(path string) (uint32, fs.FileMode, error) {
		if entry, ok := overrides[path]; ok {
			return entry.uid, entry.mode, nil
		}
		return 0, 0o755, nil
	}
}

func recordCommands(cmds *[]string) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		*cmds = append(*cmds, strings.Join(append([]string{name}, args...), " "))
		return nil, nil
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s mode = %#o, want %#o", path, got, want)
	}
}

func assertNoEntries(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("%s is not empty: %v", dir, names)
	}
}

func TestInstallLinuxUser(t *testing.T) {
	opts := baseOptions(t)
	opts.XDGConfigHome = ""
	var cmds []string
	opts.Run = recordCommands(&cmds)

	res, err := Install(opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	path := filepath.Join(opts.Root, "home/alice/.config/systemd/user/agent-peer.service")
	want, err := SystemdUserUnit(opts.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, path); got != want {
		t.Errorf("unit content mismatch:\n got %q\nwant %q", got, want)
	}
	assertMode(t, path, 0o644)

	wantCmds := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable agent-peer.service",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if !slices.Equal(res.Commands, wantCmds) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, wantCmds)
	}
	if res.UnitPath != "/home/alice/.config/systemd/user/agent-peer.service" {
		t.Errorf("UnitPath = %q", res.UnitPath)
	}
	if res.StartHint != "systemctl --user start agent-peer.service" {
		t.Errorf("StartHint = %q", res.StartHint)
	}
	if res.ConfigPath != "" || res.ConfigNew {
		t.Errorf("user install touched the system config: %+v", res)
	}
}

func TestInstallLinuxUserHonoursXDGConfigHome(t *testing.T) {
	t.Run("absolute", func(t *testing.T) {
		opts := baseOptions(t)
		opts.XDGConfigHome = "/x/cfg"
		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
		path := filepath.Join(opts.Root, "x/cfg/systemd/user/agent-peer.service")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("unit not written under XDG_CONFIG_HOME: %v", err)
		}
	})

	t.Run("relative falls back", func(t *testing.T) {
		opts := baseOptions(t)
		opts.XDGConfigHome = "rel"
		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
		path := filepath.Join(opts.Root, "home/alice/.config/systemd/user/agent-peer.service")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("unit not written under Home/.config: %v", err)
		}
	})
}

func TestInstallLinuxSystem(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	var cmds []string
	opts.Run = recordCommands(&cmds)

	res, err := Install(opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	unitPath := filepath.Join(opts.Root, "etc/systemd/system/agent-peer.service")
	want, err := SystemdSystemUnit(opts.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, unitPath); got != want {
		t.Errorf("unit content mismatch:\n got %q\nwant %q", got, want)
	}

	configPath := filepath.Join(opts.Root, "etc/agent-peer/config.yaml")
	if got := mustReadFile(t, configPath); got != SystemConfigTemplate() {
		t.Errorf("config content = %q, want the template", got)
	}
	assertMode(t, configPath, 0o600)
	if !res.ConfigNew {
		t.Error("ConfigNew = false, want true")
	}
	if res.ConfigPath != "/etc/agent-peer/config.yaml" {
		t.Errorf("ConfigPath = %q", res.ConfigPath)
	}

	wantCmds := []string{
		"systemctl daemon-reload",
		"systemctl enable agent-peer.service",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if res.StartHint != "systemctl start agent-peer.service" {
		t.Errorf("StartHint = %q", res.StartHint)
	}
}

func TestInstallKeepsExistingSystemConfig(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	configPath := filepath.Join(opts.Root, "etc/agent-peer/config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = "broker:\n  alias: x\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Install(opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := mustReadFile(t, configPath); got != existing {
		t.Errorf("config was overwritten: %q", got)
	}
	if res.ConfigNew {
		t.Error("ConfigNew = true, want false")
	}
	if res.ConfigPath != "/etc/agent-peer/config.yaml" {
		t.Errorf("ConfigPath = %q", res.ConfigPath)
	}
}

func TestInstallSystemRequiresRoot(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 1000
	called := false
	opts.Run = func(string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	_, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), "must run as root") {
		t.Fatalf("Install error = %v, want the root requirement", err)
	}
	assertNoEntries(t, opts.Root)
	if called {
		t.Error("Install ran a command before the root check")
	}
}

func TestInstallUserRejectsRoot(t *testing.T) {
	opts := baseOptions(t)
	opts.System = false
	opts.EUID = 0
	called := false
	opts.Run = func(string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	_, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), "must not run as root") {
		t.Fatalf("Install error = %v, want the root rejection", err)
	}
	assertNoEntries(t, opts.Root)
	if called {
		t.Error("Install ran a command before the root check")
	}
}

func TestInstallSystemRejectsWritableBinary(t *testing.T) {
	t.Run("directory owned by another account", func(t *testing.T) {
		opts := baseOptions(t)
		opts.System = true
		opts.EUID = 0
		opts.Owner = fakeOwner(map[string]fakeOwnerEntry{
			"/usr/local/bin": {uid: 1000, mode: 0o755},
		})
		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "/usr/local/bin") {
			t.Fatalf("Install error = %v, want one naming /usr/local/bin", err)
		}
	})

	t.Run("binary group writable", func(t *testing.T) {
		opts := baseOptions(t)
		opts.System = true
		opts.EUID = 0
		opts.Owner = fakeOwner(map[string]fakeOwnerEntry{
			opts.Exe: {uid: 0, mode: 0o775},
		})
		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), opts.Exe) {
			t.Fatalf("Install error = %v, want one naming %s", err, opts.Exe)
		}
	})

	t.Run("safe binary succeeds", func(t *testing.T) {
		opts := baseOptions(t)
		opts.System = true
		opts.EUID = 0
		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
	})
}

func TestInstallSystemChecksResolvedPath(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	const resolved = "/home/alice/bin/agent-peer"
	opts.EvalSymlinks = func(string) (string, error) { return resolved, nil }
	opts.Owner = fakeOwner(map[string]fakeOwnerEntry{
		resolved: {uid: 1000, mode: 0o755},
	})

	_, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), resolved) {
		t.Fatalf("Install error = %v, want one naming %s", err, resolved)
	}
}

func TestInstallDarwinSystemWritesResolvedExecutable(t *testing.T) {
	const (
		link     = "/Users/alice/bin/agent-peer"
		resolved = "/opt/agent-peer/agent-peer"
	)
	darwinSystem := func(t *testing.T) Options {
		t.Helper()
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.System = true
		opts.EUID = 0
		opts.UID = 0
		opts.Exe = link
		opts.EvalSymlinks = func(string) (string, error) { return resolved, nil }
		return opts
	}

	t.Run("plist names the resolved path", func(t *testing.T) {
		opts := darwinSystem(t)
		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
		plist := filepath.Join(opts.Root, "Library/LaunchDaemons/io.github.wangzw.agent-peer.plist")
		want, err := LaunchDaemonPlist(resolved)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustReadFile(t, plist); got != want {
			t.Errorf("plist content mismatch:\n got %q\nwant %q", got, want)
		}
		if got := mustReadFile(t, plist); strings.Contains(got, link) {
			t.Errorf("plist names the unresolved link %q:\n%s", link, got)
		}
	})

	t.Run("resolved target outside root is refused", func(t *testing.T) {
		opts := darwinSystem(t)
		opts.Owner = fakeOwner(map[string]fakeOwnerEntry{
			resolved: {uid: 1000, mode: 0o755},
		})
		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), resolved) {
			t.Fatalf("Install error = %v, want one naming %s", err, resolved)
		}
		assertNoEntries(t, opts.Root)
	})
}

func TestInstallRejectsTempBinary(t *testing.T) {
	for _, system := range []struct {
		name string
		set  bool
	}{{"user", false}, {"system", true}} {
		t.Run(system.name, func(t *testing.T) {
			opts := baseOptions(t)
			opts.Exe = "/tmp/go-build123/exe/agent-peer"
			opts.TempDir = "/tmp"
			opts.System = system.set
			if system.set {
				opts.EUID = 0
			}
			_, err := Install(opts)
			if err == nil || !strings.Contains(err.Error(), "temporary") {
				t.Fatalf("Install error = %v, want a temporary-directory rejection", err)
			}
		})
	}
}

func TestInstallLinuxWithoutSystemctl(t *testing.T) {
	opts := baseOptions(t)
	opts.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	_, err := Install(opts)
	if err == nil {
		t.Fatal("Install succeeded without systemctl")
	}
	assertNoEntries(t, opts.Root)
}

func TestInstallSystemctlFailureReported(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = func(name string, args ...string) ([]byte, error) {
		if slices.Contains(args, "enable") {
			return []byte("Failed to enable unit"), errors.New("exit status 1")
		}
		return nil, nil
	}

	res, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), "Failed to enable unit") {
		t.Fatalf("Install error = %v, want the systemctl output", err)
	}
	if res.UnitPath == "" {
		t.Error("Result.UnitPath is empty after a failed enable")
	}
}

func TestInstallDarwinUser(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = false
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"

	res, err := Install(opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	plist := filepath.Join(opts.Root, "Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist")
	want, err := LaunchAgentPlist(opts.Exe, "/Users/alice/Library/Logs/agent-peer/broker.log")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, plist); got != want {
		t.Errorf("plist content mismatch:\n got %q\nwant %q", got, want)
	}

	logDir := filepath.Join(opts.Root, "Users/alice/Library/Logs/agent-peer")
	fi, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("log directory missing: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("%s is not a directory", logDir)
	}
	assertMode(t, logDir, 0o700)

	if len(res.Commands) != 0 {
		t.Errorf("Commands = %q, want none", res.Commands)
	}
	wantHint := "launchctl bootstrap gui/501 " + "/Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist"
	if res.StartHint != wantHint {
		t.Errorf("StartHint = %q, want %q", res.StartHint, wantHint)
	}
}

func TestInstallDarwinSystem(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.EUID = 0
	opts.UID = 0
	opts.Home = "/var/root"

	res, err := Install(opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	plist := filepath.Join(opts.Root, "Library/LaunchDaemons/io.github.wangzw.agent-peer.plist")
	want, err := LaunchDaemonPlist(opts.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, plist); got != want {
		t.Errorf("plist content mismatch:\n got %q\nwant %q", got, want)
	}

	logDir := filepath.Join(opts.Root, "var/log/agent-peer")
	if fi, err := os.Stat(logDir); err != nil || !fi.IsDir() {
		t.Errorf("log directory %s: %v", logDir, err)
	}
	assertMode(t, logDir, 0o750)

	configPath := filepath.Join(opts.Root, "etc/agent-peer/config.yaml")
	if got := mustReadFile(t, configPath); got != SystemConfigTemplate() {
		t.Errorf("config content = %q, want the template", got)
	}
	assertMode(t, configPath, 0o600)

	if len(res.Commands) != 0 {
		t.Errorf("Commands = %q, want none on macOS", res.Commands)
	}
	if res.StartHint != "launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist" {
		t.Errorf("StartHint = %q", res.StartHint)
	}
}

func TestInstallOverwritesUnit(t *testing.T) {
	opts := baseOptions(t)
	path := filepath.Join(opts.Root, "home/alice/.config/systemd/user/agent-peer.service")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale unit"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	want, err := SystemdUserUnit(opts.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, path); got != want {
		t.Errorf("stale unit was not replaced: %q", got)
	}
}

func TestInstallUnsupportedOS(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "freebsd"
	if _, err := Install(opts); err == nil {
		t.Fatal("Install succeeded on an unsupported platform")
	}
}
