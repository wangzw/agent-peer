package service

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestValidAdminGroupName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"agent-peer", true},
		{"broker_ops-1", true},
		{"_ops", true},
		{"a", true},
		{"", false},
		{"-ops", false},
		{"Ops", false},
		{"1ops", false},
		{"op s", false},
		{"ops/1", false},
		{"ops.1", false},
	}
	for _, tc := range cases {
		if got := ValidAdminGroupName(tc.name); got != tc.want {
			t.Errorf("ValidAdminGroupName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestInstallSystemRejectsInvalidAdminGroup(t *testing.T) {
	opts := baseOptions(t)
	opts.System = true
	opts.EUID = 0
	opts.AdminGroup = "-bad"
	opts.GroupGID = nil
	opts.Run = func(name string, args ...string) ([]byte, error) {
		t.Errorf("ran %s %s before rejecting the group name", name, strings.Join(args, " "))
		return nil, nil
	}

	if _, err := Install(opts); err == nil || !strings.Contains(err.Error(), "invalid administration group name") {
		t.Fatalf("Install error = %v, want the invalid name error", err)
	}
	assertNoEntries(t, opts.Root)
}

func TestAdminGroupName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"", DefaultAdminGroup},
		{"broker-ops", "broker-ops"},
	}
	for _, tc := range cases {
		if got := adminGroupName(Options{AdminGroup: tc.name}); got != tc.want {
			t.Errorf("adminGroupName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEnsureAdminGroupDarwinCreatesTheGroup(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.GroupGID = nil
	var cmds []string
	created := false
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch name {
		case "dscl":
			if !created {
				return []byte("No such key: PrimaryGroupID"), exitError(t, 1)
			}
			return []byte("PrimaryGroupID: 999\n"), nil
		case "dseditgroup":
			created = true
			return nil, nil
		}
		return nil, nil
	}

	var res Result
	gid, err := ensureAdminGroup(opts, &res)
	if err != nil {
		t.Fatalf("ensureAdminGroup: %v", err)
	}
	if gid != 999 {
		t.Errorf("group id = %d, want 999", gid)
	}
	wantCmds := []string{
		"dscl . -read /Groups/agent-peer",
		"dseditgroup -o create -r agent-peer administrators agent-peer",
		"dscl . -read /Groups/agent-peer",
	}
	if !slices.Equal(cmds, wantCmds) {
		t.Errorf("commands = %q, want %q", cmds, wantCmds)
	}
	if want := []string{"dseditgroup -o create -r agent-peer administrators agent-peer"}; !slices.Equal(res.Commands, want) {
		t.Errorf("Result.Commands = %q, want %q", res.Commands, want)
	}
}

func TestEnsureAdminGroupDarwinUsesAnExistingGroup(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.GroupGID = nil
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		return []byte("PrimaryGroupID: 20\n"), nil
	}

	var res Result
	gid, err := ensureAdminGroup(opts, &res)
	if err != nil {
		t.Fatalf("ensureAdminGroup: %v", err)
	}
	if gid != 20 {
		t.Errorf("group id = %d, want 20", gid)
	}
	if want := []string{"dscl . -read /Groups/agent-peer"}; !slices.Equal(cmds, want) {
		t.Errorf("commands = %q, want %q", cmds, want)
	}
	if len(res.Commands) != 0 {
		t.Errorf("Result.Commands = %q, want no state change", res.Commands)
	}
}

func TestEnsureAdminGroupDarwinNamesTheRequestedGroup(t *testing.T) {
	opts := baseOptions(t)
	opts.GOOS = "darwin"
	opts.System = true
	opts.AdminGroup = "broker-ops"
	opts.GroupGID = nil
	var cmds []string
	opts.Run = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.Join(append([]string{name}, args...), " "))
		switch name {
		case "dscl":
			return []byte("No such key: PrimaryGroupID"), exitError(t, 1)
		case "dseditgroup":
			return nil, nil
		}
		return nil, nil
	}

	var res Result
	if _, err := ensureAdminGroup(opts, &res); err == nil {
		t.Fatal("ensureAdminGroup succeeded although the group was not found after creation")
	}
	want := []string{
		"dscl . -read /Groups/broker-ops",
		"dseditgroup -o create -r agent-peer administrators broker-ops",
	}
	if !slices.Equal(cmds[:2], want) {
		t.Errorf("commands = %q, want the first two to be %q", cmds, want)
	}
}

func TestLookupGroupLinux(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GroupGID = nil
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if name != "getent" || !slices.Equal(args, []string{"group", "staff"}) {
				t.Errorf("ran %s %q, want getent group staff", name, args)
			}
			return []byte("staff:x:50:\n"), nil
		}
		gid, exists, err := lookupGroup(opts, "staff")
		if err != nil || !exists || gid != 50 {
			t.Errorf("lookupGroup = (%d, %v, %v), want (50, true, nil)", gid, exists, err)
		}
	})

	t.Run("absent", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GroupGID = nil
		opts.Run = func(string, ...string) ([]byte, error) { return []byte(""), exitError(t, 2) }
		gid, exists, err := lookupGroup(opts, "nobody")
		if err != nil || exists || gid != 0 {
			t.Errorf("lookupGroup = (%d, %v, %v), want (0, false, nil)", gid, exists, err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GroupGID = nil
		opts.Run = func(string, ...string) ([]byte, error) { return []byte("nsswitch broken"), errors.New("exit status 1") }
		if _, _, err := lookupGroup(opts, "staff"); err == nil || !strings.Contains(err.Error(), "nsswitch broken") {
			t.Errorf("lookupGroup error = %v, want the command output", err)
		}
	})

	t.Run("unexpected output", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GroupGID = nil
		opts.Run = func(string, ...string) ([]byte, error) { return []byte("staff\n"), nil }
		if _, _, err := lookupGroup(opts, "staff"); err == nil || !strings.Contains(err.Error(), "unexpected output") {
			t.Errorf("lookupGroup error = %v, want the unexpected output", err)
		}
	})
}

func TestLookupGroupDarwin(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.GroupGID = nil
		opts.Run = func(name string, args ...string) ([]byte, error) {
			if name != "dscl" || !slices.Equal(args, []string{".", "-read", "/Groups/staff"}) {
				t.Errorf("ran %s %q, want the dscl read", name, args)
			}
			return []byte("PrimaryGroupID: 20\n"), nil
		}
		gid, exists, err := lookupGroup(opts, "staff")
		if err != nil || !exists || gid != 20 {
			t.Errorf("lookupGroup = (%d, %v, %v), want (20, true, nil)", gid, exists, err)
		}
	})

	t.Run("absent", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.GroupGID = nil
		opts.Run = func(string, ...string) ([]byte, error) { return []byte("No such key"), exitError(t, 1) }
		gid, exists, err := lookupGroup(opts, "nobody")
		if err != nil || exists || gid != 0 {
			t.Errorf("lookupGroup = (%d, %v, %v), want (0, false, nil)", gid, exists, err)
		}
	})

	t.Run("missing attribute", func(t *testing.T) {
		opts := baseOptions(t)
		opts.GOOS = "darwin"
		opts.GroupGID = nil
		opts.Run = func(string, ...string) ([]byte, error) { return []byte("RecordName: staff\n"), nil }
		if _, _, err := lookupGroup(opts, "staff"); err == nil || !strings.Contains(err.Error(), "no PrimaryGroupID") {
			t.Errorf("lookupGroup error = %v, want the missing attribute", err)
		}
	})
}
