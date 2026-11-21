package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// setOnlineCall is one recorded runSetOnline call.
type setOnlineCall struct {
	system   bool
	explicit bool
	online   bool
	out      io.Writer
}

// serviceSubcommand returns the named child of "peers service".
func serviceSubcommand(t *testing.T, a *app, name string) *cobra.Command {
	t.Helper()

	service, _, err := a.newRootCommand().Find([]string{"service"})
	if err != nil || service == nil || service.Name() != "service" {
		t.Fatalf("find service command: %v", err)
	}
	child, _, err := service.Find([]string{name})
	if err != nil || child == nil || child.Name() != name {
		t.Fatalf("find service %s command: %v", name, err)
	}
	return child
}

func TestOfflineOnlineRegistered(t *testing.T) {
	a, _, _ := newTermcmdApp(&termcmdStub{})
	for _, name := range []string{"offline", "online"} {
		serviceSubcommand(t, a, name)
	}
}

func TestTopLevelUsageListsOfflineOnline(t *testing.T) {
	a, stdout, stderr := newTermcmdApp(&termcmdStub{})

	if got := a.run(context.Background(), []string{"--help"}); got != exitOK {
		t.Fatalf("run(--help) = %d, want %d", got, exitOK)
	}
	if !strings.Contains(stdout.String(), "service") {
		t.Errorf("top-level usage does not mention the service group:\n%s", stdout.String())
	}

	stdout.Reset()
	if got := a.run(context.Background(), []string{"service", "--help"}); got != exitOK {
		t.Fatalf("run(service --help) = %d, want %d", got, exitOK)
	}
	for _, want := range []string{"offline", "online"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("service usage does not mention %q:\n%s", want, stdout.String())
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

			if got := a.run(context.Background(), []string{"service", tc.command}); got != exitOK {
				t.Fatalf("run(service %s) = %d, want %d (stderr %q)", tc.command, got, exitOK, stderr.String())
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

	if got := a.run(context.Background(), []string{"service", "offline"}); got != exitFailure {
		t.Fatalf("run(service offline) = %d, want %d", got, exitFailure)
	}
	if want := "peers service offline: permission_denied: write denied\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestOfflineRejectsArguments checks that both commands accept no arguments
// and -h prints their usage.
func TestOfflineRejectsArguments(t *testing.T) {
	for _, command := range []string{"offline", "online"} {
		t.Run(command, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), []string{"service", command, "extra"}); got != exitUsage {
				t.Fatalf("run(service %s extra) = %d, want %d", command, got, exitUsage)
			}
			if len(stub.setOnlineCalls) != 0 {
				t.Fatalf("runSetOnline called %d times, want 0", len(stub.setOnlineCalls))
			}
			if want := usageDiagnostic("peers service "+command, `unexpected argument "extra"`); stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}

			a, stdout, _ := newTermcmdApp(&termcmdStub{})
			if got := a.run(context.Background(), []string{"service", command, "-h"}); got != exitOK {
				t.Fatalf("run(service %s -h) = %d, want %d", command, got, exitOK)
			}
			if want := "peers service " + command; !strings.Contains(stdout.String(), want) {
				t.Fatalf("usage output = %q, want it to contain %q", stdout.String(), want)
			}
		})
	}
}
