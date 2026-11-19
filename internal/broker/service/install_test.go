package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// baseOptions returns user-level Linux options rooted in a temporary directory.
// Probe reports no Broker and ReadyTimeout keeps the readiness wait short; a
// test that exercises the start overrides both.
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
		Probe:    func(string) (int, bool) { return 0, false },
		Owner:    fakeOwner(nil),
		EvalSymlinks: func(path string) (string, error) {
			return path, nil
		},
		ReadyTimeout: 20 * time.Millisecond,
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

// recordCommandsUntil records every command and sets started when trigger
// appears in the arguments, standing in for the service manager starting the
// Broker.
func recordCommandsUntil(cmds *[]string, started *bool, trigger string) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		*cmds = append(*cmds, strings.Join(append([]string{name}, args...), " "))
		if slices.Contains(args, "show") {
			return []byte("4242\n"), nil
		}
		if slices.Contains(args, trigger) {
			*started = true
		}
		return nil, nil
	}
}

// probeAfterStart reports no Broker before the service starts and the given pid
// once it has.
func probeAfterStart(started *bool, pid int) func(string) (int, bool) {
	return func(string) (int, bool) {
		if *started {
			return pid, true
		}
		return 0, false
	}
}

// exitError returns the error of a process that exited with code, so the
// launchctl exit-code handling is exercised by a real *exec.ExitError.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("sh -c 'exit %d' succeeded", code)
	}
	return err
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
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)

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
		"systemctl --user restart agent-peer.service",
		"systemctl --user show --property MainPID --value agent-peer.service",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if !slices.Equal(res.Commands, wantCmds[:3]) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, wantCmds[:3])
	}
	if res.UnitPath != "/home/alice/.config/systemd/user/agent-peer.service" {
		t.Errorf("UnitPath = %q", res.UnitPath)
	}
	if res.Domain != "" {
		t.Errorf("Domain = %q, want empty on Linux", res.Domain)
	}
	if res.ConfigPath != "" || res.ConfigNew {
		t.Errorf("user install touched the system config: %+v", res)
	}
}

func TestInstallLinuxUserHonoursXDGConfigHome(t *testing.T) {
	install := func(t *testing.T, opts Options) {
		t.Helper()
		var cmds []string
		started := false
		opts.Run = recordCommandsUntil(&cmds, &started, "restart")
		opts.Probe = probeAfterStart(&started, 4242)
		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
	}

	t.Run("absolute", func(t *testing.T) {
		opts := baseOptions(t)
		opts.XDGConfigHome = "/x/cfg"
		install(t, opts)
		path := filepath.Join(opts.Root, "x/cfg/systemd/user/agent-peer.service")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("unit not written under XDG_CONFIG_HOME: %v", err)
		}
	})

	t.Run("relative falls back", func(t *testing.T) {
		opts := baseOptions(t)
		opts.XDGConfigHome = "rel"
		install(t, opts)
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
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)

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
		"systemctl restart agent-peer.service",
		"systemctl show --property MainPID --value agent-peer.service",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
}

func TestInstallLinuxStepFailures(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
		want    []string
	}{
		{
			name:    "daemon-reload",
			trigger: "daemon-reload",
			want:    []string{"systemctl --user daemon-reload"},
		},
		{
			name:    "enable",
			trigger: "enable",
			want: []string{
				"systemctl --user daemon-reload",
				"systemctl --user enable agent-peer.service",
			},
		},
		{
			name:    "restart",
			trigger: "restart",
			want: []string{
				"systemctl --user daemon-reload",
				"systemctl --user enable agent-peer.service",
				"systemctl --user restart agent-peer.service",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOptions(t)
			var cmds []string
			opts.Run = func(name string, args ...string) ([]byte, error) {
				cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
				if slices.Contains(args, tc.trigger) {
					return []byte("failed step"), errors.New("exit status 1")
				}
				return nil, nil
			}

			res, err := Install(opts)
			if err == nil || !strings.Contains(err.Error(), "failed step") {
				t.Fatalf("Install error = %v, want the %s failure", err, tc.trigger)
			}
			if !slices.Equal(cmds, tc.want) {
				t.Errorf("commands = %q, want %q", cmds, tc.want)
			}
			if res.UnitPath == "" {
				t.Error("Result.UnitPath is empty after a failed start")
			}
		})
	}
}

func TestInstallKeepsExistingSystemConfig(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	var cmds []string
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)
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
		var cmds []string
		started := false
		opts.Run = recordCommandsUntil(&cmds, &started, "restart")
		opts.Probe = probeAfterStart(&started, 4242)
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
		var cmds []string
		started := false
		opts.Run = func(name string, args ...string) ([]byte, error) {
			cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
			switch {
			case len(args) == 2 && args[0] == "print" && args[1] == "system/io.github.wangzw.agent-peer":
				return []byte("state = running\n\tpid = 4242\n"), nil
			case len(args) > 0 && args[0] == "bootout":
				return nil, exitError(t, 3)
			case len(args) > 0 && args[0] == "bootstrap":
				started = true
			}
			return nil, nil
		}
		opts.Probe = probeAfterStart(&started, 4242)
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

func TestInstallDarwinUser(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = false
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"
	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch {
		case len(args) == 2 && args[0] == "print" && args[1] == "gui/501/io.github.wangzw.agent-peer":
			return []byte("state = running\n\tpid = 4242\n"), nil
		case len(args) == 2 && args[0] == "bootout":
			return nil, exitError(t, 3)
		case len(args) > 0 && args[0] == "bootstrap":
			started = true
		}
		return nil, nil
	}
	opts.Probe = probeAfterStart(&started, 4242)

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

	wantCmds := []string{
		"launchctl print gui/501",
		"launchctl bootout gui/501/io.github.wangzw.agent-peer",
		"launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist",
		"launchctl print gui/501/io.github.wangzw.agent-peer",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if !slices.Equal(res.Commands, wantCmds[1:3]) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, wantCmds[1:3])
	}
	if res.Domain != "gui/501" {
		t.Errorf("Domain = %q, want gui/501", res.Domain)
	}
}

func TestInstallDarwinWithoutGUIDomain(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"
	opts.Run = func(string, ...string) ([]byte, error) {
		return []byte("Could not find domain"), exitError(t, 113)
	}

	_, err := Install(opts)
	if err == nil {
		t.Fatal("Install succeeded without a gui domain")
	}
	for _, want := range []string{"graphical login session", "--system"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	assertNoEntries(t, opts.Root)
}

func TestInstallDarwinBootoutFailure(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		if len(args) > 0 && args[0] == "bootout" {
			return []byte("Boot-out failed: 5: Input/output error"), exitError(t, 5)
		}
		return nil, nil
	}

	_, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), "bootout") {
		t.Fatalf("Install error = %v, want the bootout failure", err)
	}
	wantCmds := []string{
		"launchctl print gui/501",
		"launchctl bootout gui/501/io.github.wangzw.agent-peer",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	plist := filepath.Join(opts.Root, "Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist")
	if _, err := os.Stat(plist); err != nil {
		t.Errorf("plist after a failed bootout: %v", err)
	}
}

func TestInstallDarwinBootstrapFailureRollsBackThePlist(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch {
		case len(args) > 0 && args[0] == "bootout":
			return nil, exitError(t, 3)
		case len(args) > 0 && args[0] == "bootstrap":
			return []byte("Bootstrap failed: 5: Input/output error"), exitError(t, 5)
		}
		return nil, nil
	}

	_, err := Install(opts)
	if err == nil || !strings.Contains(err.Error(), "Bootstrap failed") {
		t.Fatalf("Install error = %v, want the bootstrap failure", err)
	}
	wantCmds := []string{
		"launchctl print gui/501",
		"launchctl bootout gui/501/io.github.wangzw.agent-peer",
		"launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	plist := filepath.Join(opts.Root, "Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist")
	if _, err := os.Stat(plist); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("plist was not rolled back: %v", err)
	}
}

func TestInstallDarwinBootstrapFailureReportsTheOutputWhenRollbackFails(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.EUID = 501
	opts.UID = 501
	opts.Home = "/Users/alice"
	plist := filepath.Join(opts.Root, "Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist")
	opts.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case len(args) > 0 && args[0] == "bootout":
			return nil, exitError(t, 3)
		case len(args) > 0 && args[0] == "bootstrap":
			// Replace the plist with a non-empty directory so the rollback
			// cannot remove it.
			_ = os.Remove(plist)
			if err := os.Mkdir(plist, 0o755); err != nil {
				t.Fatalf("creating %s: %v", plist, err)
			}
			if err := os.WriteFile(filepath.Join(plist, "keep"), []byte("x"), 0o644); err != nil {
				t.Fatalf("writing marker: %v", err)
			}
			return []byte("Bootstrap failed: 5: Input/output error"), exitError(t, 5)
		}
		return nil, nil
	}

	_, err := Install(opts)
	if err == nil {
		t.Fatal("Install succeeded after a failed bootstrap")
	}
	for _, want := range []string{"Bootstrap failed", "could not remove", plist} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestInstallDarwinSystem(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.EUID = 0
	opts.UID = 0
	opts.Home = "/var/root"
	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch {
		case len(args) == 2 && args[0] == "print" && args[1] == "system/io.github.wangzw.agent-peer":
			return []byte("state = running\n\tpid = 4242\n"), nil
		case len(args) > 0 && args[0] == "bootout":
			return nil, exitError(t, 3)
		case len(args) > 0 && args[0] == "bootstrap":
			started = true
		}
		return nil, nil
	}
	opts.Probe = probeAfterStart(&started, 4242)

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

	wantCmds := []string{
		"launchctl bootout system/io.github.wangzw.agent-peer",
		"launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist",
		"launchctl print system/io.github.wangzw.agent-peer",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if !slices.Equal(res.Commands, wantCmds[:2]) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, wantCmds[:2])
	}
	if res.Domain != "system" {
		t.Errorf("Domain = %q, want system", res.Domain)
	}
}

func TestInstallRejectsForeignBroker(t *testing.T) {
	t.Run("linux service-managed Broker is replaced", func(t *testing.T) {
		opts := baseOptions(t)
		const pid = 4242
		opts.Probe = func(string) (int, bool) { return pid, true }
		var cmds []string
		opts.Run = func(name string, args ...string) ([]byte, error) {
			cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
			if slices.Contains(args, "show") {
				return []byte(fmt.Sprintf("%d\n", pid)), nil
			}
			return nil, nil
		}

		res, err := Install(opts)
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		wantCmds := []string{
			"systemctl --user show --property MainPID --value agent-peer.service",
			"systemctl --user daemon-reload",
			"systemctl --user enable agent-peer.service",
			"systemctl --user restart agent-peer.service",
			"systemctl --user show --property MainPID --value agent-peer.service",
		}
		if !slices.Equal(cmds, wantCmds) {
			t.Errorf("commands = %q, want %q", cmds, wantCmds)
		}
		if res.UnitPath == "" {
			t.Error("UnitPath is empty")
		}
	})

	t.Run("linux service pid differs from the socket peer", func(t *testing.T) {
		opts := baseOptions(t)
		opts.Probe = func(string) (int, bool) { return 4242, true }
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if slices.Contains(args, "show") {
				return []byte("9999\n"), nil
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "not started by the agent-peer service") {
			t.Fatalf("Install error = %v, want the foreign-Broker rejection", err)
		}
		for _, want := range []string{"service pid 9999", "socket peer pid 4242", "stop it before installing"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
		assertNoEntries(t, opts.Root)
	})

	t.Run("linux unit not running", func(t *testing.T) {
		opts := baseOptions(t)
		opts.Probe = func(string) (int, bool) { return 4242, true }
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if slices.Contains(args, "show") {
				return []byte("0\n"), nil
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "not started by the agent-peer service") {
			t.Fatalf("Install error = %v, want the foreign-Broker rejection", err)
		}
		assertNoEntries(t, opts.Root)
	})

	t.Run("linux system pid differs", func(t *testing.T) {
		opts := baseOptions(t)
		opts.System = true
		opts.EUID = 0
		opts.Probe = func(path string) (int, bool) {
			if path != "/run/agent-peer/broker.sock" {
				t.Errorf("probe path = %q", path)
			}
			return 4242, true
		}
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if slices.Contains(args, "show") {
				return []byte("9999\n"), nil
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "system-level Broker") {
			t.Fatalf("Install error = %v, want the foreign system Broker rejection", err)
		}
	})

	t.Run("macOS service-managed Broker is replaced", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.EUID = 501
		opts.UID = 501
		opts.Home = "/Users/alice"
		const pid = 4242
		opts.Probe = func(string) (int, bool) { return pid, true }
		var cmds []string
		opts.Run = func(name string, args ...string) ([]byte, error) {
			cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
			switch {
			case len(args) == 2 && args[0] == "print" && args[1] == "gui/501/io.github.wangzw.agent-peer":
				return []byte(fmt.Sprintf("state = running\n\tpid = %d\n", pid)), nil
			case len(args) > 0 && args[0] == "bootout":
				return nil, exitError(t, 3)
			}
			return nil, nil
		}

		if _, err := Install(opts); err != nil {
			t.Fatalf("Install: %v", err)
		}
		wantCmds := []string{
			"launchctl print gui/501",
			"launchctl print gui/501/io.github.wangzw.agent-peer",
			"launchctl bootout gui/501/io.github.wangzw.agent-peer",
			"launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/io.github.wangzw.agent-peer.plist",
			"launchctl print gui/501/io.github.wangzw.agent-peer",
		}
		if !slices.Equal(cmds, wantCmds) {
			t.Errorf("commands = %q, want %q", cmds, wantCmds)
		}
	})

	t.Run("macOS service pid differs", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.EUID = 501
		opts.UID = 501
		opts.Home = "/Users/alice"
		opts.Probe = func(string) (int, bool) { return 4242, true }
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if len(args) == 2 && args[1] == "gui/501/io.github.wangzw.agent-peer" {
				return []byte("state = running\n\tpid = 9999\n"), nil
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "not started by the agent-peer service") {
			t.Fatalf("Install error = %v, want the foreign-Broker rejection", err)
		}
		for _, want := range []string{"service pid 9999", "socket peer pid 4242"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	})

	t.Run("macOS unit not loaded but the socket answers", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.EUID = 501
		opts.UID = 501
		opts.Home = "/Users/alice"
		opts.Probe = func(string) (int, bool) { return 4242, true }
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if len(args) == 2 && args[1] == "gui/501/io.github.wangzw.agent-peer" {
				return []byte("Could not find service"), exitError(t, 113)
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "not started by the agent-peer service") {
			t.Fatalf("Install error = %v, want the foreign-Broker rejection", err)
		}
		if !strings.Contains(err.Error(), "service pid 0") {
			t.Errorf("error %q does not name the missing service pid", err)
		}
	})

	t.Run("peer pid unknown", func(t *testing.T) {
		opts := baseOptions(t)
		opts.Probe = func(string) (int, bool) { return 0, true }
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if slices.Contains(args, "show") {
				return []byte("4242\n"), nil
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "not started by the agent-peer service") {
			t.Fatalf("Install error = %v, want the foreign-Broker rejection", err)
		}
		if !strings.Contains(err.Error(), "socket peer pid 0") {
			t.Errorf("error %q does not name the unknown peer pid", err)
		}
	})
}

func TestInstallRejectsAnAnsweringForeignBrokerAtReady(t *testing.T) {
	cases := []struct {
		name    string
		mainPID string
	}{
		{name: "service pid zero", mainPID: "0"},
		{name: "service pid differs", mainPID: "9999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOptions(t)
			probes := 0
			opts.Probe = func(string) (int, bool) {
				probes++
				if probes == 1 {
					// The pre-install check sees no Broker.
					return 0, false
				}
				// A foreign Broker answers after the service starts.
				return 6666, true
			}
			opts.Run = func(name string, args ...string) ([]byte, error) {
				if slices.Contains(args, "show") {
					return []byte(tc.mainPID + "\n"), nil
				}
				return nil, nil
			}
			opts.ReadyTimeout = 30 * time.Millisecond

			_, err := Install(opts)
			if err == nil {
				t.Fatal("Install succeeded while a foreign Broker answered on the socket")
			}
			for _, want := range []string{"not the service's process", "socket peer pid 6666", "journalctl --user -u agent-peer.service"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestInstallWaitsUntilTheServiceOwnsTheSocket(t *testing.T) {
	opts := baseOptions(t)
	probes := 0
	opts.Probe = func(string) (int, bool) {
		probes++
		if probes <= 3 {
			return 0, false
		}
		return 4242, true
	}
	opts.Run = func(name string, args ...string) ([]byte, error) {
		if slices.Contains(args, "show") {
			return []byte("4242\n"), nil
		}
		return nil, nil
	}
	opts.ReadyTimeout = time.Second

	if _, err := Install(opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if probes < 4 {
		t.Errorf("probe calls = %d, want the readiness loop to retry", probes)
	}
}

func TestInstallWaitsForTheBroker(t *testing.T) {
	t.Run("linux user", func(t *testing.T) {
		opts := baseOptions(t)
		var cmds []string
		started := false
		opts.Run = recordCommandsUntil(&cmds, &started, "restart")
		opts.Probe = func(string) (int, bool) { return 0, false }
		opts.ReadyTimeout = 30 * time.Millisecond

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("Install error = %v, want the readiness timeout", err)
		}
		if !strings.Contains(err.Error(), "journalctl --user -u agent-peer.service") {
			t.Errorf("error %q does not name the user service log", err)
		}
		if !slices.Contains(cmds, "systemctl --user restart agent-peer.service") {
			t.Errorf("commands = %q, want the restart", cmds)
		}
	})

	t.Run("darwin system", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.System = true
		opts.EUID = 0
		opts.UID = 0
		opts.Home = "/var/root"
		opts.Probe = func(string) (int, bool) { return 0, false }
		opts.ReadyTimeout = 30 * time.Millisecond
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "bootout" {
				return nil, exitError(t, 3)
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("Install error = %v, want the readiness timeout", err)
		}
		if !strings.Contains(err.Error(), "/var/log/agent-peer/broker.log") {
			t.Errorf("error %q does not name the system log", err)
		}
	})

	t.Run("linux system", func(t *testing.T) {
		opts := baseOptions(t)
		opts.System = true
		opts.EUID = 0
		opts.Probe = func(string) (int, bool) { return 0, false }
		opts.ReadyTimeout = 30 * time.Millisecond

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("Install error = %v, want the readiness timeout", err)
		}
		if !strings.Contains(err.Error(), "journalctl -u agent-peer.service") {
			t.Errorf("error %q does not name the system service log", err)
		}
	})

	t.Run("darwin user", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.EUID = 501
		opts.UID = 501
		opts.Home = "/Users/alice"
		opts.Probe = func(string) (int, bool) { return 0, false }
		opts.ReadyTimeout = 30 * time.Millisecond
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "bootout" {
				return nil, exitError(t, 3)
			}
			return nil, nil
		}

		_, err := Install(opts)
		if err == nil || !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("Install error = %v, want the readiness timeout", err)
		}
		if !strings.Contains(err.Error(), "/Users/alice/Library/Logs/agent-peer/broker.log") {
			t.Errorf("error %q does not name the user log", err)
		}
	})
}

func TestInstallOverwritesUnit(t *testing.T) {
	opts := baseOptions(t)
	var cmds []string
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)
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
