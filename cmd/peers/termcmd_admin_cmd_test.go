package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// configCalls counts every configuration dependency call a stub recorded.
func configCalls(s *termcmdStub) int {
	return len(s.configGetCalls) + len(s.configSetCalls)
}

func TestAdminCommandsRegistered(t *testing.T) {
	a, _, _ := newTermcmdApp(&termcmdStub{})
	root := a.newRootCommand()
	for _, name := range []string{"broker", "key"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("root command has no %q command", name)
		}
	}
	if _, ok := subcommands["config"]; !ok {
		t.Errorf("subcommands[%q] is missing", "config")
	}
}

func TestConfigGetCallsRunner(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantKey string
	}{
		{"all items", []string{"config", "get"}, ""},
		{"one item", []string{"config", "get", "broker.log_level"}, "broker.log_level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitOK, stderr.String())
			}
			if len(stub.configGetCalls) != 1 {
				t.Fatalf("runConfigGet called %d times, want 1", len(stub.configGetCalls))
			}
			call := stub.configGetCalls[0]
			if call.key != tc.wantKey {
				t.Fatalf("runConfigGet key = %q, want %q", call.key, tc.wantKey)
			}
			if call.out != stdout {
				t.Fatalf("runConfigGet got writer %T, want app.stdout", call.out)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
			}
		})
	}
}

func TestConfigSetCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"config", "set", "broker.log_level", "debug"}); got != exitOK {
		t.Fatalf("run(config set) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.configSetCalls) != 1 {
		t.Fatalf("runConfigSet called %d times, want 1", len(stub.configSetCalls))
	}
	call := stub.configSetCalls[0]
	if call.key != "broker.log_level" || call.value != "debug" {
		t.Fatalf("runConfigSet got (%q, %q), want (broker.log_level, debug)", call.key, call.value)
	}
	if call.out != stdout {
		t.Fatalf("runConfigSet got writer %T, want app.stdout", call.out)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
}

func TestAdminCommandErrorsExitOne(t *testing.T) {
	errDenied := errors.New("permission_denied: not allowed")
	cases := []struct {
		name    string
		args    []string
		command string
		prepare func(*termcmdStub)
	}{
		{
			name: "config list", args: []string{"config", "list"}, command: "config list",
			prepare: func(s *termcmdStub) { s.configGetErr = errDenied },
		},
		{
			name: "config get", args: []string{"config", "get", "broker.alias"}, command: "config get",
			prepare: func(s *termcmdStub) { s.configGetErr = errDenied },
		},
		{
			name: "config set", args: []string{"config", "set", "k", "v"}, command: "config set",
			prepare: func(s *termcmdStub) { s.configSetErr = errDenied },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			tc.prepare(stub)
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitFailure {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitFailure)
			}
			if want := "peers " + tc.command + ": permission_denied: not allowed\n"; stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestAdminUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		usage   string
		command string
	}{
		{"config without verb", []string{"config"}, configUsage, "config"},
		{"config unknown verb", []string{"config", "bogus"}, configUsage, "config"},
		{"config get unknown flag", []string{"config", "get", "--bogus"}, configUsage, "config get"},
		{"config get extra argument", []string{"config", "get", "a", "b"}, configUsage, "config get"},
		{"config set missing value", []string{"config", "set", "key"}, configUsage, "config set"},
		{"config set extra argument", []string{"config", "set", "k", "v", "x"}, configUsage, "config set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitUsage)
			}
			if calls := configCalls(stub); calls != 0 {
				t.Fatalf("dependencies called %d times, want 0", calls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), tc.usage) {
				t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
			}
		})
	}
}

func TestAdminHelp(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		usage string
	}{
		{"config -h", []string{"config", "-h"}, configUsage},
		{"config --help", []string{"config", "--help"}, configUsage},
		{"config get -h", []string{"config", "get", "-h"}, configUsage},
		{"config get after key", []string{"config", "get", "broker.alias", "--help"}, configUsage},
		{"config set --help", []string{"config", "set", "--help"}, configUsage},
		{"config set after value", []string{"config", "set", "k", "v", "-h"}, configUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitOK, stderr.String())
			}
			if calls := configCalls(stub); calls != 0 {
				t.Fatalf("dependencies called %d times, want 0", calls)
			}
			if stdout.String() != tc.usage {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tc.usage)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}
