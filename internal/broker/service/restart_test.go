package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRestartLinuxUserRestartsAndWaits(t *testing.T) {
	opts := baseOptions(t)
	unit := "/home/alice/.config/systemd/user/agent-peer.service"
	writeInstalledFile(t, opts, unit)

	var cmds []string
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)

	restarted, err := Restart(context.Background(), opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !restarted {
		t.Fatal("restarted = false, want true for an installed unit")
	}
	if len(cmds) == 0 || cmds[0] != "systemctl --user restart agent-peer.service" {
		t.Fatalf("commands = %q, want the user restart first", cmds)
	}
	if !slices.Contains(cmds, "systemctl --user show --property MainPID --value agent-peer.service") {
		t.Fatalf("commands = %q, want the readiness probe", cmds)
	}
}

func TestRestartLinuxSystemRestarts(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	writeInstalledFile(t, opts, "/etc/systemd/system/agent-peer.service")

	var cmds []string
	started := false
	opts.Run = recordCommandsUntil(&cmds, &started, "restart")
	opts.Probe = probeAfterStart(&started, 4242)

	restarted, err := Restart(context.Background(), opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !restarted {
		t.Fatal("restarted = false, want true for an installed unit")
	}
	if len(cmds) == 0 || cmds[0] != "systemctl restart agent-peer.service" {
		t.Fatalf("commands = %q, want the system restart first", cmds)
	}
}

func TestRestartLinuxNotInstalled(t *testing.T) {
	opts := baseOptions(t)
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		return nil, errors.New("exit status 1")
	}

	restarted, err := Restart(context.Background(), opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted {
		t.Fatal("restarted = true, want false when no unit is installed")
	}
	for _, cmd := range cmds {
		if strings.Contains(cmd, "restart") {
			t.Fatalf("commands = %q, want no restart command", cmds)
		}
	}
}

func TestRestartDarwinSystemBootstrapsThePlist(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.EUID = 0
	opts.UID = 0
	plist := "/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist"
	writeInstalledFile(t, opts, plist)

	var cmds []string
	started := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch {
		case slices.Contains(args, "print"):
			return []byte("pid = 4242\n"), nil
		case slices.Contains(args, "bootstrap"):
			started = true
			return nil, nil
		default:
			return nil, nil
		}
	}
	opts.Probe = probeAfterStart(&started, 4242)

	restarted, err := Restart(context.Background(), opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !restarted {
		t.Fatal("restarted = false, want true for an installed plist")
	}
	for _, want := range []string{
		"launchctl bootout system/io.github.wangzw.agent-peer",
		"launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist",
	} {
		if !slices.Contains(cmds, want) {
			t.Fatalf("commands = %q, want %q", cmds, want)
		}
	}
}

func TestRestartDarwinNotInstalled(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.EUID = 0
	opts.UID = 0
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		return nil, errors.New("not loaded")
	}

	restarted, err := Restart(context.Background(), opts)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted {
		t.Fatal("restarted = true, want false when nothing is installed")
	}
	for _, cmd := range cmds {
		if strings.Contains(cmd, "bootout") || strings.Contains(cmd, "bootstrap") {
			t.Fatalf("commands = %q, want no bootout or bootstrap", cmds)
		}
	}
}

func TestRestartStopsOnCancelledContext(t *testing.T) {
	opts := baseOptions(t)
	writeInstalledFile(t, opts, "/home/alice/.config/systemd/user/agent-peer.service")
	opts.Run = func(string, ...string) ([]byte, error) {
		t.Fatal("a command ran with a cancelled context")
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	restarted, err := Restart(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Restart error = %v, want context.Canceled", err)
	}
	if restarted {
		t.Fatal("restarted = true on a cancelled context")
	}
}

func TestRestartRejectsRootMismatch(t *testing.T) {
	cases := []struct {
		name   string
		system bool
		euid   int
	}{
		{"system without root", true, 1000},
		{"user as root", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOptions(t)
			opts.System = tc.system
			opts.EUID = tc.euid
			if _, err := Restart(context.Background(), opts); err == nil {
				t.Fatal("Restart succeeded, want a level mismatch error")
			}
		})
	}
}
