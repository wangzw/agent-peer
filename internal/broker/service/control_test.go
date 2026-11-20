package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// writeInstalledUnit writes the unit file or plist opts names, so Installed
// accepts the level without querying the service manager.
func writeInstalledUnit(t *testing.T, opts Options) string {
	t.Helper()
	path, err := unitPath(opts)
	if err != nil {
		t.Fatalf("unitPath: %v", err)
	}
	fsPath := rooted(opts, path)
	if err := os.MkdirAll(filepath.Dir(fsPath), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(fsPath), err)
	}
	if err := os.WriteFile(fsPath, []byte("unit\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", fsPath, err)
	}
	return path
}

// linuxControlOptions returns user-level Linux options whose StatusProbe
// reports a healthy Broker. A test that exercises commands starts from these.
func linuxControlOptions(t *testing.T) Options {
	t.Helper()
	opts := baseOptions(t)
	opts.StatusProbe = func(string) (DegradedState, bool, error) {
		return DegradedState{}, false, nil
	}
	return opts
}

func TestStartLinuxUser(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)

	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		if name == "systemctl" && slices.Contains(args, "start") {
			started = true
		}
		if name == "systemctl" && slices.Contains(args, "show") {
			if started {
				return []byte("4242\n"), nil
			}
			return []byte("0\n"), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) {
		if started {
			return 4242, true
		}
		return 0, false
	}

	res, err := Start(opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if want := []string{"systemctl --user start agent-peer.service"}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	wantCmds := []string{
		"systemctl --user start agent-peer.service",
		"systemctl --user show --property MainPID --value agent-peer.service",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if res.Domain != "" {
		t.Errorf("Domain = %q, want empty on Linux", res.Domain)
	}
}

func TestStartLinuxSystem(t *testing.T) {
	opts := linuxControlOptions(t)
	opts.System = true
	opts.EUID = 0
	opts.UID = 0
	writeInstalledUnit(t, opts)

	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		if slices.Contains(args, "start") {
			started = true
		}
		if slices.Contains(args, "show") {
			return []byte("4242\n"), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) { return 4242, started }

	res, err := Start(opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if want := []string{"systemctl start agent-peer.service"}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if len(cmds) == 0 || cmds[0] != "systemctl start agent-peer.service" {
		t.Errorf("commands = %q, want the system-level start first", cmds)
	}
}

func TestStopLinux(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)

	var cmds []string
	opts.Run = recordCommands(&cmds)

	res, err := Stop(opts)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if want := []string{"systemctl --user stop agent-peer.service"}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if !slices.Equal(cmds, res.Commands) {
		t.Errorf("commands = %q, want %q", cmds, res.Commands)
	}
}

func TestRestartLinux(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)

	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		if slices.Contains(args, "restart") {
			started = true
		}
		if slices.Contains(args, "show") && started {
			return []byte("4242\n"), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) { return 4242, started }

	res, err := Restart(opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := []string{"systemctl --user restart agent-peer.service"}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	wantCmds := append(want, "systemctl --user show --property MainPID --value agent-peer.service")
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
}

func TestStartNotInstalled(t *testing.T) {
	for _, system := range []bool{false, true} {
		t.Run(fmt.Sprintf("system=%t", system), func(t *testing.T) {
			opts := linuxControlOptions(t)
			opts.System = system
			var cmds []string
			opts.Run = func(name string, args ...string) ([]byte, error) {
				cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
				return nil, exitError(t, 1)
			}

			_, err := Start(opts)
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("Start error = %v, want ErrNotInstalled", err)
			}
			level := "user-level"
			if system {
				level = "system-level"
			}
			if !strings.Contains(err.Error(), level) {
				t.Errorf("error %q does not name the %s level", err, level)
			}
			for _, cmd := range cmds {
				for _, verb := range []string{" start", " stop", " restart"} {
					if strings.Contains(cmd, verb) {
						t.Errorf("Start ran %q before reporting the service is not installed", cmd)
					}
				}
			}
		})
	}
}

func TestStopAndRestartNotInstalled(t *testing.T) {
	for _, verb := range []string{"Stop", "Restart"} {
		t.Run(verb, func(t *testing.T) {
			opts := linuxControlOptions(t)
			opts.Run = func(string, ...string) ([]byte, error) { return nil, exitError(t, 1) }
			var err error
			if verb == "Stop" {
				_, err = Stop(opts)
			} else {
				_, err = Restart(opts)
			}
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("%s error = %v, want ErrNotInstalled", verb, err)
			}
		})
	}
}

func TestStartReadinessTimeoutKeepsTheServiceEnabled(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)
	opts.ReadyTimeout = 20 * time.Millisecond

	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch {
		case name == "systemctl" && slices.Contains(args, "show"):
			return []byte("0\n"), nil
		case name == "journalctl":
			return []byte(fmt.Sprintf(`{"time":%q,"event":"startup_failed","error":"bind: address already in use"}`,
				time.Now().UTC().Format(time.RFC3339Nano))), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) { return 0, false }

	res, err := Start(opts)
	if err == nil {
		t.Fatal("Start succeeded although the Broker never answered")
	}
	if !strings.Contains(err.Error(), "startup_failed record says: bind: address already in use") {
		t.Errorf("error = %q, want the startup_failed reason", err)
	}
	if want := []string{"systemctl --user start agent-peer.service"}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	for _, cmd := range cmds {
		if strings.Contains(cmd, "disable") || strings.Contains(cmd, "stop") {
			t.Errorf("a failed start ran %q; it must not stop or disable the service", cmd)
		}
	}
}

func TestStartReportsDegraded(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)

	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		if slices.Contains(args, "start") {
			started = true
		}
		if slices.Contains(args, "show") {
			return []byte("4242\n"), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) { return 4242, started }
	want := DegradedState{Level: localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY, Step: "listen", Reason: "listen :17443: address already in use", Fix: "change broker.listen"}
	opts.StatusProbe = func(string) (DegradedState, bool, error) { return want, true, nil }

	res, err := Start(opts)
	var degraded *DegradedError
	if !errors.As(err, &degraded) {
		t.Fatalf("Start error = %v, want *DegradedError", err)
	}
	if degraded.State != want {
		t.Errorf("DegradedError.State = %+v, want %+v", degraded.State, want)
	}
	if wantCmds := []string{"systemctl --user start agent-peer.service"}; !slices.Equal(res.Commands, wantCmds) {
		t.Errorf("Result.Commands = %q, want the complete result %q", res.Commands, wantCmds)
	}
}

func TestStartReportsStatusProbeError(t *testing.T) {
	opts := linuxControlOptions(t)
	writeInstalledUnit(t, opts)
	// Leave room for one retry, so the test also proves the probe is retried
	// within the remaining readiness time.
	opts.ReadyTimeout = 150 * time.Millisecond
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		if slices.Contains(args, "start") {
			started = true
		}
		if slices.Contains(args, "show") {
			return []byte("4242\n"), nil
		}
		return nil, nil
	}
	opts.Probe = func(string) (int, bool) { return 4242, started }
	probes := 0
	opts.StatusProbe = func(string) (DegradedState, bool, error) {
		probes++
		return DegradedState{}, false, errors.New("connection reset")
	}

	_, err := Start(opts)
	if err == nil {
		t.Fatal("Start error = nil, want the status probe error")
	}
	for _, want := range []string{"connection reset", "could not be read", "agent-peer status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Start error = %q, want it to contain %q", err, want)
		}
	}
	if probes < 2 {
		t.Errorf("StatusProbe calls = %d, want it retried until the readiness deadline", probes)
	}
	var degraded *DegradedError
	if errors.As(err, &degraded) {
		t.Fatalf("Start reported *DegradedError for a probe error: %v", err)
	}
}

// TestStartAndRestartRetryTheStatusProbe checks that both commands retry a
// Status call that fails once, as Install does, instead of failing the command.
func TestStartAndRestartRetryTheStatusProbe(t *testing.T) {
	for _, command := range []string{"start", "restart"} {
		t.Run(command, func(t *testing.T) {
			opts := linuxControlOptions(t)
			writeInstalledUnit(t, opts)
			opts.ReadyTimeout = 250 * time.Millisecond
			started := false
			opts.Run = func(name string, args ...string) ([]byte, error) {
				if slices.Contains(args, command) {
					started = true
				}
				if slices.Contains(args, "show") && started {
					return []byte("4242\n"), nil
				}
				return nil, nil
			}
			opts.Probe = func(string) (int, bool) { return 4242, started }
			probes := 0
			opts.StatusProbe = func(string) (DegradedState, bool, error) {
				probes++
				if probes == 1 {
					return DegradedState{}, false, errors.New("connection reset")
				}
				return DegradedState{}, false, nil
			}

			var err error
			if command == "start" {
				_, err = Start(opts)
			} else {
				_, err = Restart(opts)
			}
			if err != nil {
				t.Fatalf("%s: %v", command, err)
			}
			if probes < 2 {
				t.Errorf("StatusProbe calls = %d, want a retry", probes)
			}
		})
	}
}

// darwinFake stands in for launchctl. It tracks whether the job is loaded and
// the pid launchd reports, so a test can drive start, stop and restart through
// the same sequence launchd would see. stillLoadedPolls makes launchd's
// asynchronous teardown visible: after a bootout, that many print probes still
// report the job loaded before one reports it gone.
type darwinFake struct {
	t                 *testing.T
	cmds              []string
	loaded            bool
	pid               int
	bootstrapFailures int
	bootstraps        int
	stillLoadedPolls  int
	// remainingLoadedPolls counts the still-loaded reports left after the most
	// recent bootout.
	remainingLoadedPolls int
	// unloadedAt is the index in cmds of the print probe that first reported the
	// job gone after the most recent bootout, or -1 before it has.
	unloadedAt int
}

func (f *darwinFake) run(name string, args ...string) ([]byte, error) {
	f.cmds = append(f.cmds, strings.Join(append([]string{name}, args...), " "))
	if name != "launchctl" || len(args) == 0 {
		return nil, nil
	}
	switch args[0] {
	case "print":
		if len(args) != 2 {
			return nil, nil
		}
		switch args[1] {
		case "gui/501", "system":
			return []byte("domain\n"), nil
		case "gui/501/" + Label, "system/" + Label:
			if f.loaded {
				return []byte(fmt.Sprintf("state = running\n\tpid = %d\n", f.pid)), nil
			}
			if f.remainingLoadedPolls > 0 {
				f.remainingLoadedPolls--
				return []byte("state = running\n"), nil
			}
			f.unloadedAt = len(f.cmds) - 1
			return []byte("Could not find service"), exitError(f.t, 113)
		default:
			return []byte("Could not find domain"), exitError(f.t, 113)
		}
	case "bootout":
		if !f.loaded {
			return []byte("No such process"), exitError(f.t, 3)
		}
		f.loaded = false
		f.pid = 0
		f.remainingLoadedPolls = f.stillLoadedPolls
		f.unloadedAt = -1
		return nil, nil
	case "enable":
		return nil, nil
	case "kickstart":
		f.loaded = true
		f.pid = 4242
		f.remainingLoadedPolls = 0
		return nil, nil
	case "bootstrap":
		f.bootstraps++
		if f.bootstrapFailures > 0 {
			f.bootstrapFailures--
			return []byte("Bootstrap failed: 5: Input/output error"), exitError(f.t, 5)
		}
		f.loaded = true
		f.pid = 4242
		f.remainingLoadedPolls = 0
		return nil, nil
	}
	return nil, nil
}

func darwinControlOptions(t *testing.T) (Options, *darwinFake) {
	t.Helper()
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.Home = "/Users/alice"
	opts.UID = 501
	opts.EUID = 501
	opts.Exe = "/usr/local/bin/agent-peer"
	opts.TempDir = "/tmp"
	opts.StatusProbe = func(string) (DegradedState, bool, error) {
		return DegradedState{}, false, nil
	}
	fake := &darwinFake{t: t, unloadedAt: -1}
	opts.Run = fake.run
	opts.Probe = func(string) (int, bool) { return fake.pid, fake.loaded }
	return opts, fake
}

func TestStartDarwinBootstrapsAnUnloadedService(t *testing.T) {
	opts, _ := darwinControlOptions(t)
	plist := writeInstalledUnit(t, opts)

	res, err := Start(opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []string{
		"launchctl enable gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if res.Domain != "gui/501" {
		t.Errorf("Domain = %q, want gui/501", res.Domain)
	}
}

func TestStartDarwinKickstartsALoadedService(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111

	res, err := Start(opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []string{"launchctl kickstart gui/501/" + Label}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if !fake.loaded || fake.pid != 4242 {
		t.Errorf("after kickstart loaded=%t pid=%d, want loaded with pid 4242", fake.loaded, fake.pid)
	}
}

func TestStopDarwinWaitsForUnload(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111

	res, err := Stop(opts)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{"launchctl bootout gui/501/" + Label}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if fake.loaded {
		t.Error("the job is still loaded after Stop")
	}
	for _, cmd := range res.Commands {
		if strings.Contains(cmd, "enable") || strings.Contains(cmd, "bootstrap") || strings.Contains(cmd, "disable") {
			t.Errorf("Stop ran %q; it must not change the enabled state", cmd)
		}
	}
}

// TestStopDarwinWaitsUntilLaunchdReportsUnloaded covers launchd's asynchronous
// teardown: bootout returns while print still reports the job. Stop must keep
// probing until launchd reports it gone instead of returning right after the
// bootout.
func TestStopDarwinWaitsUntilLaunchdReportsUnloaded(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111
	fake.stillLoadedPolls = 3

	if _, err := Stop(opts); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	bootoutAt := slices.Index(fake.cmds, "launchctl bootout gui/501/"+Label)
	if bootoutAt < 0 {
		t.Fatalf("commands = %q, want a bootout", fake.cmds)
	}
	if fake.unloadedAt < 0 {
		t.Fatal("Stop returned before launchctl print reported the job unloaded")
	}
	if got := fake.unloadedAt - bootoutAt - 1; got != fake.stillLoadedPolls {
		t.Errorf("print probes reporting the job still loaded after bootout = %d, want %d; commands = %q",
			got, fake.stillLoadedPolls, fake.cmds)
	}
}

func TestStopDarwinIgnoresBootoutExitThree(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	// The job is not loaded; launchctl bootout reports exit 3.
	fake.loaded = false

	res, err := Stop(opts)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if want := []string{"launchctl bootout gui/501/" + Label}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
}

func TestRestartDarwinReloadsThePlist(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	plist := writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111

	res, err := Restart(opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := []string{
		"launchctl bootout gui/501/" + Label,
		"launchctl enable gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
	if !fake.loaded || fake.pid != 4242 {
		t.Errorf("after restart loaded=%t pid=%d, want loaded with pid 4242", fake.loaded, fake.pid)
	}
}

func TestRestartDarwinNotLoadedSkipsBootoutExitThree(t *testing.T) {
	opts, _ := darwinControlOptions(t)
	plist := writeInstalledUnit(t, opts)

	res, err := Restart(opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := []string{
		"launchctl bootout gui/501/" + Label,
		"launchctl enable gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	}
	if !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
}

// TestRestartDarwinBootstrapsAfterLaunchdReportsUnloaded is the restart half of
// the asynchronous teardown check: the bootstrap that loads the plist again
// must come after launchctl has reported the old job gone, not right after the
// bootout returned.
func TestRestartDarwinBootstrapsAfterLaunchdReportsUnloaded(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	plist := writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111
	fake.stillLoadedPolls = 3

	if _, err := Restart(opts); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	bootoutAt := slices.Index(fake.cmds, "launchctl bootout gui/501/"+Label)
	if bootoutAt < 0 {
		t.Fatalf("commands = %q, want a bootout", fake.cmds)
	}
	bootstrapAt := slices.Index(fake.cmds, "launchctl bootstrap gui/501 "+plist)
	if bootstrapAt < 0 {
		t.Fatalf("commands = %q, want a bootstrap", fake.cmds)
	}
	if fake.unloadedAt < 0 {
		t.Fatal("Restart bootstrapped before launchctl print reported the job unloaded")
	}
	if bootstrapAt < fake.unloadedAt {
		t.Errorf("commands = %q, want the bootstrap after the print at %d reported the job gone", fake.cmds, fake.unloadedAt)
	}
	if got := fake.unloadedAt - bootoutAt - 1; got != fake.stillLoadedPolls {
		t.Errorf("print probes reporting the job still loaded after bootout = %d, want %d; commands = %q",
			got, fake.stillLoadedPolls, fake.cmds)
	}
}

// TestRestartDarwinRefusesAMissingPlist checks the case Installed accepts: the
// job is loaded but its plist was deleted. A restart that booted the job out
// first could not bootstrap the missing file, so it must refuse before running
// any service-manager command and leave the running service alone.
func TestRestartDarwinRefusesAMissingPlist(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	path := writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111
	if err := os.Remove(rooted(opts, path)); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}

	res, err := Restart(opts)
	if err == nil {
		t.Fatal("Restart succeeded although the plist is missing")
	}
	for _, want := range []string{"plist", "service install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if !fake.loaded || fake.pid != 111 {
		t.Errorf("after the refused restart loaded=%t pid=%d, want the untouched loaded job with pid 111", fake.loaded, fake.pid)
	}
	for _, cmd := range fake.cmds {
		if strings.Contains(cmd, "bootout") || strings.Contains(cmd, "bootstrap") || strings.Contains(cmd, "disable") {
			t.Errorf("Restart ran %q before checking the plist exists", cmd)
		}
	}
	if res.Domain != "gui/501" {
		t.Errorf("Domain = %q, want gui/501 on the failure path", res.Domain)
	}
}

// TestRestartDarwinReadinessFailureKeepsTheServiceEnabled checks that a restart
// whose Broker never answers reports the startup_failed reason and leaves the
// freshly loaded service enabled: restart is not an installation and must not
// change the job's start-at-login state.
func TestRestartDarwinReadinessFailureKeepsTheServiceEnabled(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111
	opts.ReadyTimeout = 20 * time.Millisecond
	opts.Probe = func(string) (int, bool) { return 0, false }

	logPath := rooted(opts, filepath.Join(opts.Home, "Library", "Logs", "agent-peer", "broker.log"))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatalf("create log directory: %v", err)
	}
	// The Broker writes the record after the restart begins, as it does in
	// production; a record written before Restart's own start time is ignored.
	baseRun := fake.run
	opts.Run = func(name string, args ...string) ([]byte, error) {
		out, err := baseRun(name, args...)
		if err == nil && name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
			record := fmt.Sprintf(`{"time":%q,"event":"startup_failed","error":"bind: address already in use"}`,
				time.Now().UTC().Format(time.RFC3339Nano))
			if writeErr := os.WriteFile(logPath, []byte(record+"\n"), 0o600); writeErr != nil {
				t.Errorf("write %s: %v", logPath, writeErr)
			}
		}
		return out, err
	}

	res, err := Restart(opts)
	if err == nil {
		t.Fatal("Restart succeeded although the Broker never answered")
	}
	if !strings.Contains(err.Error(), "startup_failed record says: bind: address already in use") {
		t.Errorf("error = %q, want the startup_failed reason", err)
	}
	if res.Domain != "gui/501" {
		t.Errorf("Domain = %q, want gui/501 on the failure path", res.Domain)
	}
	for _, cmd := range fake.cmds {
		if strings.Contains(cmd, "disable") {
			t.Errorf("a failed restart ran %q; it must not disable the service", cmd)
		}
	}
}

func TestStartDarwinRetriesBootstrapExitFive(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	writeInstalledUnit(t, opts)
	fake.loaded = false
	fake.bootstrapFailures = 1

	if _, err := Start(opts); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if fake.bootstraps != 2 {
		t.Errorf("bootstrap attempts = %d, want 2", fake.bootstraps)
	}
}

func TestRestartDarwinReportsDegradedWithTheFullResult(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	plist := writeInstalledUnit(t, opts)
	fake.loaded = true
	fake.pid = 111
	want := DegradedState{Level: localpb.DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY, Step: "audit", Reason: "audit log is not writable", Fix: "check the audit log path"}
	opts.StatusProbe = func(string) (DegradedState, bool, error) { return want, true, nil }

	res, err := Restart(opts)
	var degraded *DegradedError
	if !errors.As(err, &degraded) {
		t.Fatalf("Restart error = %v, want *DegradedError", err)
	}
	if degraded.State != want {
		t.Errorf("DegradedError.State = %+v, want %+v", degraded.State, want)
	}
	wantCmds := []string{
		"launchctl bootout gui/501/" + Label,
		"launchctl enable gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	}
	if !slices.Equal(res.Commands, wantCmds) {
		t.Errorf("Result.Commands = %q, want the complete result %q", res.Commands, wantCmds)
	}
	if res.Domain != "gui/501" {
		t.Errorf("Domain = %q, want gui/501", res.Domain)
	}
	for _, cmd := range fake.cmds {
		if strings.Contains(cmd, "disable") {
			t.Errorf("a degraded restart ran %q; it must leave the service enabled", cmd)
		}
	}
}

func TestStartDarwinNotInstalled(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	fake.loaded = false

	if _, err := Start(opts); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Start error = %v, want ErrNotInstalled", err)
	}
	for _, cmd := range fake.cmds {
		if strings.Contains(cmd, "bootstrap") || strings.Contains(cmd, "kickstart") || strings.Contains(cmd, "enable") {
			t.Errorf("Start ran %q before reporting the service is not installed", cmd)
		}
	}
}

// TestStartAndRestartDarwinWithoutAGuiDomainReportTheGraphicalSession checks
// that the commands that need a domain explain the control context instead of
// the installation's advice, which stop's no-op wording already matches.
func TestStartAndRestartDarwinWithoutAGuiDomainReportTheGraphicalSession(t *testing.T) {
	verbs := []struct {
		name string
		run  func(Options) (Result, error)
	}{
		{"Start", Start},
		{"Restart", Restart},
	}
	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			opts, fake := darwinControlOptions(t)
			opts.UID = 777
			opts.EUID = 777
			writeInstalledUnit(t, opts)

			_, err := verb.run(opts)
			if err == nil {
				t.Fatalf("%s succeeded without a gui/777 domain", verb.name)
			}
			if errors.Is(err, ErrNotInstalled) {
				t.Fatalf("%s error = %v, want a missing-domain error", verb.name, err)
			}
			for _, want := range []string{"gui/777", "graphical login session"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			for _, unwanted := range []string{"service install", "--system"} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("error = %q, want no installation wording such as %q", err, unwanted)
				}
			}
			for _, cmd := range fake.cmds {
				if strings.Contains(cmd, "bootstrap") || strings.Contains(cmd, "kickstart") || strings.Contains(cmd, "enable") || strings.Contains(cmd, "bootout") {
					t.Errorf("%s ran %q without a gui domain", verb.name, cmd)
				}
			}
		})
	}
}

// TestStopAndRestartDarwinUnloadTimeoutAdvice checks that the unload timeout
// tells the operator which command to retry instead of the installation's
// advice.
func TestStopAndRestartDarwinUnloadTimeoutAdvice(t *testing.T) {
	verbs := []struct {
		name  string
		run   func(Options) (Result, error)
		again string
	}{
		{"Stop", Stop, "run service stop again"},
		{"Restart", Restart, "run service restart again"},
	}
	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			opts, fake := darwinControlOptions(t)
			plist := writeInstalledUnit(t, opts)
			fake.loaded = true
			fake.pid = 111
			fake.stillLoadedPolls = 1000
			opts.StopTimeout = 20 * time.Millisecond

			_, err := verb.run(opts)
			if err == nil {
				t.Fatalf("%s succeeded although launchd never reported the job unloaded", verb.name)
			}
			if !strings.Contains(err.Error(), "was not unloaded within") {
				t.Errorf("error = %q, want the unload timeout", err)
			}
			if !strings.Contains(err.Error(), verb.again) {
				t.Errorf("error = %q, want it to say %q", err, verb.again)
			}
			if strings.Contains(err.Error(), "install again") {
				t.Errorf("error = %q, want no installation advice", err)
			}
			for _, cmd := range fake.cmds {
				if verb.name == "Restart" && strings.Contains(cmd, "bootstrap gui/501 "+plist) {
					t.Errorf("Restart ran %q while the old job was still loaded", cmd)
				}
			}
		})
	}
}

func TestStopDarwinWithoutAGuiDomainIsANoOp(t *testing.T) {
	opts, fake := darwinControlOptions(t)
	opts.UID = 777
	opts.EUID = 777
	writeInstalledUnit(t, opts)

	// No gui/777 domain exists; the test fake reports only gui/501.
	res, err := Stop(opts)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(res.Commands) != 0 {
		t.Errorf("Result.Commands = %q, want no service-manager command", res.Commands)
	}
	if res.Domain != "" {
		t.Errorf("Domain = %q, want empty when no domain exists", res.Domain)
	}
	for _, cmd := range fake.cmds {
		if strings.Contains(cmd, "bootout") {
			t.Errorf("Stop ran %q without a gui domain", cmd)
		}
	}
}
