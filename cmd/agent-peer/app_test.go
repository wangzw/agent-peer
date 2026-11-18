package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The release contract: the version variable keeps its package path, name and
// type so -ldflags "-X main.version=<version>" keeps working.
var _ string = version

func newDispatchApp(ver string) (*app, *bytes.Buffer, *bytes.Buffer, *stubDeps) {
	stub := &stubDeps{}
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	a := &app{
		stdin:   strings.NewReader(""),
		stdout:  stdout,
		stderr:  stderr,
		version: ver,
		deps:    stub.deps(),
	}
	return a, stdout, stderr, stub
}

func TestVersionPrintsInjectedVersion(t *testing.T) {
	a, stdout, stderr, _ := newDispatchApp("v1.2.3")

	if got := a.run(context.Background(), []string{"--version"}); got != exitOK {
		t.Fatalf("run(--version) = %d, want %d", got, exitOK)
	}
	if want := "agent-peer v1.2.3\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestMainVersionContract(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want the default %q", version, "dev")
	}

	a, stdout, stderr, _ := newDispatchApp(version)
	if got := a.run(context.Background(), []string{"--version"}); got != exitOK {
		t.Fatalf("run(--version) = %d, want %d", got, exitOK)
	}
	if want := "agent-peer dev\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionRejectsExtraArguments(t *testing.T) {
	a, _, _, _ := newDispatchApp("v1.2.3")

	if got := a.run(context.Background(), []string{"--version", "x"}); got != exitUsage {
		t.Fatalf("run(--version x) = %d, want %d", got, exitUsage)
	}
}

func TestNoArgumentsPrintsUsageToStderr(t *testing.T) {
	a, stdout, stderr, _ := newDispatchApp("dev")

	if got := a.run(context.Background(), nil); got != exitUsage {
		t.Fatalf("run() = %d, want %d", got, exitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: agent-peer") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestHelpPrintsUsageToStdout(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			a, stdout, stderr, _ := newDispatchApp("dev")

			if got := a.run(context.Background(), []string{arg}); got != exitOK {
				t.Fatalf("run(%s) = %d, want %d", arg, got, exitOK)
			}
			out := stdout.String()
			if !strings.Contains(out, "usage: agent-peer") {
				t.Fatalf("stdout = %q, want the usage text", out)
			}
			if strings.Contains(out, "internal") {
				t.Fatalf("stdout leaks the hidden internal commands: %q", out)
			}
			if strings.Contains(out, "--ready-fd") {
				t.Fatalf("stdout leaks the hidden --ready-fd flag: %q", out)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	a, _, stderr, _ := newDispatchApp("dev")

	if got := a.run(context.Background(), []string{"frobnicate"}); got != exitUsage {
		t.Fatalf("run(frobnicate) = %d, want %d", got, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q, want it to name the unknown command", stderr.String())
	}
}

func TestUnknownInternalCommandIsHidden(t *testing.T) {
	cases := [][]string{
		{"internal"},
		{"internal", "nope"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, stdout, stderr, _ := newDispatchApp("dev")

			if got := a.run(context.Background(), args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", args, got, exitUsage)
			}
			if strings.Contains(stderr.String(), "usage: agent-peer") {
				t.Fatalf("stderr = %q, want no top-level usage for the hidden group", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestSignalContextCancelsOnSIGTERM(t *testing.T) {
	ctx, stop := newSignalContext(context.Background())
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("context was not cancelled within 3s of SIGTERM")
	}
}
