package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// TestPostUpdateLaunchdRewritesThePlistAndStartsADetachedHelper checks both
// levels: the plist is replaced with this binary's format, and the reload is
// deferred to a helper that runs in its own session with its standard streams
// on the post-update log. Nothing is booted out synchronously, because that
// would stop the job that runs the Broker and this process.
func TestPostUpdateLaunchdRewritesThePlistAndStartsADetachedHelper(t *testing.T) {
	cases := []struct {
		name       string
		system     bool
		level      string
		domain     string
		plistPath  string
		auditPath  string
		helperLog  string
		plist      func(string, string) (string, error)
		domainCmds []string
	}{
		{
			name:      "user",
			system:    false,
			level:     "user",
			domain:    "gui/501",
			plistPath: "/Users/alice/Library/LaunchAgents/" + Label + ".plist",
			auditPath: "/Users/alice/.peers/audit.log",
			helperLog: "/Users/alice/Library/Logs/peers/" + LaunchdHelperLogName,
			plist:     LaunchAgentPlist,
			domainCmds: []string{
				"launchctl print gui/501",
			},
		},
		{
			name:      "system",
			system:    true,
			level:     "system",
			domain:    "system",
			plistPath: "/Library/LaunchDaemons/" + Label + ".plist",
			auditPath: "/var/log/peers/audit.log",
			helperLog: "/var/log/peers/" + LaunchdHelperLogName,
			plist:     func(exe, _ string) (string, error) { return LaunchDaemonPlist(exe) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, fake := darwinControlOptions(t)
			opts.System = tc.system
			if tc.system {
				opts.EUID = 0
			}
			writeInstalledUnit(t, opts)
			opts.PPID = func() int { return 4242 }
			var spawned *exec.Cmd
			opts.StartDetached = func(cmd *exec.Cmd) error {
				spawned = cmd
				return nil
			}

			if err := PostUpdate(opts); err != nil {
				t.Fatalf("PostUpdate: %v", err)
			}

			plistPath := filepath.Join(opts.Root, tc.plistPath)
			logPath := filepath.Join(opts.Home, "Library", "Logs", "peers", "broker.log")
			want, err := tc.plist(opts.Exe, logPath)
			if err != nil {
				t.Fatalf("render plist: %v", err)
			}
			if got := mustReadFile(t, plistPath); got != want {
				t.Errorf("plist content = %q, want %q", got, want)
			}
			if !slices.Equal(fake.cmds, tc.domainCmds) {
				t.Errorf("synchronous commands = %q, want only %q", fake.cmds, tc.domainCmds)
			}
			for _, cmd := range fake.cmds {
				if strings.Contains(cmd, "bootout") || strings.Contains(cmd, "bootstrap") {
					t.Errorf("PostUpdate ran %q synchronously; the reload must be deferred", cmd)
				}
			}

			if spawned == nil {
				t.Fatal("PostUpdate did not start the detached helper")
			}
			if spawned.SysProcAttr == nil || !spawned.SysProcAttr.Setsid {
				t.Error("the helper is not started in a new session (SysProcAttr.Setsid is false)")
			}
			if spawned.Stdin != nil {
				t.Error("the helper inherits standard input")
			}
			for name, writer := range map[string]any{"stdout": spawned.Stdout, "stderr": spawned.Stderr} {
				file, ok := writer.(*os.File)
				if !ok {
					t.Fatalf("the helper's %s is %T, want the post-update log file", name, writer)
				}
				if got, want := file.Name(), filepath.Join(opts.Root, tc.helperLog); got != want {
					t.Errorf("the helper's %s goes to %s, want %s", name, got, want)
				}
			}
			wantArgs := []string{
				opts.Exe, "internal", "launchd-reload",
				"--level", tc.level,
				"--domain", tc.domain,
				"--plist", tc.plistPath,
				"--pid", "4242",
				"--audit", tc.auditPath,
			}
			if !slices.Equal(spawned.Args, wantArgs) {
				t.Errorf("helper args = %q, want %q", spawned.Args, wantArgs)
			}
		})
	}
}

// TestLaunchdReloadWaitsForTheCallerThenBootsOutAndBootstraps checks the three
// properties the deferred reload needs: no launchctl command runs before the
// caller exits, the first is the bootout, and the bootstrap follows the unload
// wait. A gate holds the caller-exit wait open to prove the ordering.
func TestLaunchdReloadWaitsForTheCallerThenBootsOutAndBootstraps(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	fake.loaded = true
	fake.pid = 111

	waited := make(chan struct{})
	gate := make(chan struct{})
	var waitedPID int
	opts.WaitProcessExit = func(pid int, _ time.Duration) error {
		waitedPID = pid
		close(waited)
		<-gate
		return nil
	}

	auditPath := filepath.Join(t.TempDir(), "audit.log")
	done := make(chan error, 1)
	go func() {
		done <- LaunchdReload(opts, "system", "/Library/LaunchDaemons/"+Label+".plist", 4242, auditPath)
	}()

	<-waited
	if len(fake.cmds) != 0 {
		t.Fatalf("launchctl ran %q before the caller exited", fake.cmds)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("LaunchdReload: %v", err)
	}
	if waitedPID != 4242 {
		t.Errorf("waited for pid %d, want 4242", waitedPID)
	}

	want := []string{
		"launchctl bootout system/" + Label,
		"launchctl print system/" + Label,
		"launchctl bootstrap system /Library/LaunchDaemons/" + Label + ".plist",
	}
	if !slices.Equal(fake.cmds, want) {
		t.Errorf("commands = %q, want %q", fake.cmds, want)
	}
}

// TestLaunchdReloadLogsAWaitTimeoutAndReloadsAnyway checks that a Broker that
// does not exit within the bound is logged and then stopped by the bootout,
// instead of leaving the service unloaded forever.
func TestLaunchdReloadLogsAWaitTimeoutAndReloadsAnyway(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	fake.loaded = true
	fake.pid = 111
	opts.WaitProcessExit = func(int, time.Duration) error {
		return errors.New("process is still running")
	}
	var logs []string
	opts.Logf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	if err := LaunchdReload(opts, "system", "/Library/LaunchDaemons/"+Label+".plist", 4242, ""); err != nil {
		t.Fatalf("LaunchdReload: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "still running") {
		t.Errorf("logs = %q, want one line about the caller that did not exit", logs)
	}
	if len(fake.cmds) == 0 || fake.cmds[0] != "launchctl bootout system/"+Label {
		t.Errorf("commands = %q, want the bootout after the logged wait", fake.cmds)
	}
}

// TestLaunchdReloadRecordsAFailedBootstrapInTheAudit checks the second phase's
// failure path: the caller already has the success response, so the failure is
// recorded in the audit log and reported on standard error (the post-update
// log) instead of being returned to a caller.
func TestLaunchdReloadRecordsAFailedBootstrapInTheAudit(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	fake.loaded = true
	fake.pid = 111
	fake.bootstrapFailures = 1
	opts.BootstrapTimeout = time.Nanosecond
	opts.WaitProcessExit = func(int, time.Duration) error { return nil }
	auditPath := filepath.Join(t.TempDir(), "audit.log")

	err := LaunchdReload(opts, "system", "/Library/LaunchDaemons/"+Label+".plist", 4242, auditPath)
	if err == nil {
		t.Fatal("LaunchdReload succeeded although bootstrap failed")
	}
	for _, want := range []string{"launchctl bootstrap system", "Input/output error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	data, readErr := os.ReadFile(auditPath)
	if readErr != nil {
		t.Fatalf("read audit log: %v", readErr)
	}
	for _, want := range []string{
		`"event":"update_failed"`,
		"reloading the service definition after the update failed",
		"launchctl bootstrap",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("audit log = %q, want it to contain %q", data, want)
		}
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
