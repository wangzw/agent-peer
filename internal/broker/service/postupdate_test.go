package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPostUpdateLinuxRewritesTheUnit checks both levels: the main unit is
// replaced with this binary's format, a drop-in beside it is left untouched,
// and the only command run is daemon-reload. The Broker is not restarted.
func TestPostUpdateLinuxRewritesTheUnit(t *testing.T) {
	cases := []struct {
		name    string
		system  bool
		unit    func(string) (string, error)
		path    string
		wantCmd string
	}{
		{
			name:    "user",
			system:  false,
			unit:    SystemdUserUnit,
			path:    "home/alice/.config/systemd/user/peers.service",
			wantCmd: "systemctl --user daemon-reload",
		},
		{
			name:    "system",
			system:  true,
			unit:    SystemdSystemUnit,
			path:    "etc/systemd/system/peers.service",
			wantCmd: "systemctl daemon-reload",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOptions(t)
			opts.System = tc.system
			if tc.system {
				opts.EUID = 0
			}
			var cmds []string
			opts.Run = recordCommands(&cmds)

			unitFile := filepath.Join(opts.Root, tc.path)
			if err := os.MkdirAll(filepath.Dir(unitFile), 0o755); err != nil {
				t.Fatalf("create unit directory: %v", err)
			}
			if err := os.WriteFile(unitFile, []byte("stale unit\n"), 0o644); err != nil {
				t.Fatalf("write stale unit: %v", err)
			}
			const dropIn = "[Service]\nEnvironment=HTTPS_PROXY=http://proxy.example:8080\n"
			dropInPath := filepath.Join(filepath.Dir(unitFile), UnitName+".d", "proxy.conf")
			if err := os.MkdirAll(filepath.Dir(dropInPath), 0o755); err != nil {
				t.Fatalf("create drop-in directory: %v", err)
			}
			if err := os.WriteFile(dropInPath, []byte(dropIn), 0o644); err != nil {
				t.Fatalf("write drop-in: %v", err)
			}

			if err := PostUpdate(opts); err != nil {
				t.Fatalf("PostUpdate: %v", err)
			}

			want, err := tc.unit(opts.Exe)
			if err != nil {
				t.Fatalf("render unit: %v", err)
			}
			if got := mustReadFile(t, unitFile); got != want {
				t.Errorf("unit content = %q, want %q", got, want)
			}
			if got := mustReadFile(t, dropInPath); got != dropIn {
				t.Errorf("drop-in content = %q, want it left untouched as %q", got, dropIn)
			}
			if wantCmds := []string{tc.wantCmd}; !slices.Equal(cmds, wantCmds) {
				t.Errorf("commands = %q, want %q (no restart)", cmds, wantCmds)
			}
			assertNoInstallTempFiles(t, filepath.Dir(unitFile))
		})
	}
}

// TestPostUpdateLaunchdReloadsThePlist checks both levels: the plist is
// replaced with this binary's format, then the label is booted out and
// bootstrapped again so the new ProgramArguments take effect.
func TestPostUpdateLaunchdReloadsThePlist(t *testing.T) {
	cases := []struct {
		name   string
		system bool
		plist  func(string, string) (string, error)
		path   string
		want   []string
	}{
		{
			name:   "user",
			system: false,
			plist:  LaunchAgentPlist,
			path:   "Users/alice/Library/LaunchAgents/" + Label + ".plist",
			want: []string{
				"launchctl print gui/501",
				"launchctl bootout gui/501/" + Label,
				"launchctl print gui/501/" + Label,
				"launchctl bootstrap gui/501 /Users/alice/Library/LaunchAgents/" + Label + ".plist",
			},
		},
		{
			name:   "system",
			system: true,
			plist:  func(exe, _ string) (string, error) { return LaunchDaemonPlist(exe) },
			path:   "Library/LaunchDaemons/" + Label + ".plist",
			want: []string{
				"launchctl bootout system/" + Label,
				"launchctl print system/" + Label,
				"launchctl bootstrap system /Library/LaunchDaemons/" + Label + ".plist",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, fake := darwinControlOptions(t)
			opts.System = tc.system
			if tc.system {
				opts.EUID = 0
			}
			fake.loaded = true
			fake.pid = 111
			writeInstalledUnit(t, opts)

			if err := PostUpdate(opts); err != nil {
				t.Fatalf("PostUpdate: %v", err)
			}

			plistPath := filepath.Join(opts.Root, tc.path)
			logPath := filepath.Join(opts.Home, "Library", "Logs", "peers", "broker.log")
			want, err := tc.plist(opts.Exe, logPath)
			if err != nil {
				t.Fatalf("render plist: %v", err)
			}
			if got := mustReadFile(t, plistPath); got != want {
				t.Errorf("plist content = %q, want %q", got, want)
			}
			if !slices.Equal(fake.cmds, tc.want) {
				t.Errorf("commands = %q, want %q", fake.cmds, tc.want)
			}
		})
	}
}

// TestPostUpdateLinuxReportsAReloadFailure checks that a failing daemon-reload
// is reported with the command that failed, so the caller can name the reason.
func TestPostUpdateLinuxReportsAReloadFailure(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = func(string, ...string) ([]byte, error) {
		return []byte("unit reload failed"), os.ErrPermission
	}

	err := PostUpdate(opts)
	if err == nil {
		t.Fatal("PostUpdate succeeded although daemon-reload failed")
	}
	for _, want := range []string{"systemctl --user daemon-reload", "permission denied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestPostUpdateLaunchdReportsABootstrapFailure checks that a bootstrap that
// keeps failing is reported with its command and launchd's output.
func TestPostUpdateLaunchdReportsABootstrapFailure(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	fake.loaded = true
	fake.pid = 111
	fake.bootstrapFailures = 1
	opts.BootstrapTimeout = time.Nanosecond
	writeInstalledUnit(t, opts)

	err := PostUpdate(opts)
	if err == nil {
		t.Fatal("PostUpdate succeeded although bootstrap failed")
	}
	for _, want := range []string{"launchctl bootstrap gui/501", "Input/output error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestPostUpdateOptionsSystemNeedsNoHome covers the systemd case: the system
// service runs with no HOME, and rewriting its definition must not read the
// account home. The user level still reads it.
func TestPostUpdateOptionsSystemNeedsNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	opts, err := PostUpdateOptions(true)
	if err != nil {
		t.Fatalf("PostUpdateOptions(true): %v", err)
	}
	if !opts.System {
		t.Error("System = false, want the system level")
	}
	if opts.Home != "" {
		t.Errorf("Home = %q, want empty: the system level must not read the account home", opts.Home)
	}
	if opts.Exe == "" {
		t.Error("Exe is empty, so the rewritten definition would not name a binary")
	}
	if opts.Run == nil {
		t.Error("Run is nil, so the rewrite could not reload the unit database")
	}

	if _, err := PostUpdateOptions(false); err == nil {
		t.Fatal("PostUpdateOptions(false) succeeded without a home directory")
	}
}

// TestPostUpdateRejectsAnUnsupportedPlatform keeps the platform behaviour the
// other service operations have.
func TestPostUpdateRejectsAnUnsupportedPlatform(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "windows"
	if err := PostUpdate(opts); err == nil {
		t.Fatal("PostUpdate accepted an unsupported platform")
	}
}

// assertNoInstallTempFiles checks that no atomic-write temporary file survived
// the rewrite.
func assertNoInstallTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".peers-install-") {
			t.Errorf("temporary file %s survived the rewrite", entry.Name())
		}
	}
}
