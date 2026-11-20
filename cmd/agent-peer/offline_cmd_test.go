package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// setOnlineCall is one recorded runSetOnline call.
type setOnlineCall struct {
	online bool
	out    io.Writer
}

func TestOfflineOnlineRegistered(t *testing.T) {
	for _, name := range []string{"offline", "online"} {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("subcommands[%q] is missing", name)
		}
	}
}

func TestTopLevelUsageListsOfflineOnline(t *testing.T) {
	a, stdout, stderr := newTermcmdApp(&termcmdStub{})

	if got := a.run(context.Background(), []string{"--help"}); got != exitOK {
		t.Fatalf("run(--help) = %d, want %d", got, exitOK)
	}
	for _, want := range []string{"offline", "online"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

// TestOfflineOnlineCallRunner checks both commands pass the right state to the
// runner and write its output to standard output (REQ-broker-config-066).
func TestOfflineOnlineCallRunner(t *testing.T) {
	cases := []struct {
		command string
		online  bool
	}{
		{"offline", false},
		{"online", true},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), []string{tc.command}); got != exitOK {
				t.Fatalf("run(%s) = %d, want %d (stderr %q)", tc.command, got, exitOK, stderr.String())
			}
			if len(stub.setOnlineCalls) != 1 {
				t.Fatalf("runSetOnline called %d times, want 1", len(stub.setOnlineCalls))
			}
			call := stub.setOnlineCalls[0]
			if call.online != tc.online {
				t.Fatalf("runSetOnline online = %v, want %v", call.online, tc.online)
			}
			if call.out != stdout {
				t.Fatalf("runSetOnline got writer %T, want app.stdout", call.out)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
			}
		})
	}
}

// TestOfflineErrorExitsOne checks the error path shared with config set: the
// reason goes to standard error and the exit code is 1.
func TestOfflineErrorExitsOne(t *testing.T) {
	stub := &termcmdStub{setOnlineErr: errors.New("permission_denied: write denied")}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"offline"}); got != exitFailure {
		t.Fatalf("run(offline) = %d, want %d", got, exitFailure)
	}
	if want := "agent-peer offline: permission_denied: write denied\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestOfflineRejectsArguments checks that both commands accept no arguments
// and -h prints the usage.
func TestOfflineRejectsArguments(t *testing.T) {
	for _, command := range []string{"offline", "online"} {
		t.Run(command, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), []string{command, "extra"}); got != exitUsage {
				t.Fatalf("run(%s extra) = %d, want %d", command, got, exitUsage)
			}
			if len(stub.setOnlineCalls) != 0 {
				t.Fatalf("runSetOnline called %d times, want 0", len(stub.setOnlineCalls))
			}
			if !strings.Contains(stderr.String(), "agent-peer "+command+": unexpected argument \"extra\"") {
				t.Fatalf("stderr = %q, want the usage error", stderr.String())
			}

			a, stdout, _ := newTermcmdApp(&termcmdStub{})
			if got := a.run(context.Background(), []string{command, "-h"}); got != exitOK {
				t.Fatalf("run(%s -h) = %d, want %d", command, got, exitOK)
			}
			if want := "usage: agent-peer " + command + "\n"; stdout.String() != want {
				t.Fatalf("usage output = %q, want %q", stdout.String(), want)
			}
		})
	}
}
