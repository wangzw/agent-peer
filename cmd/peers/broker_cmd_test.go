package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// newBrokerCmdApp builds an app whose broker subcommand talks to stub.
func newBrokerCmdApp(stub *stubDeps) (*app, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &app{
		stdout:  stdout,
		stderr:  stderr,
		version: "v-test",
		deps:    stub.deps(),
	}, stdout, stderr
}

// brokerLastLine returns the last non-empty line of s.
func brokerLastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// TestBrokerPassesListenOverride covers REQ-broker-config-025: --listen
// replaces the configured address for this run.
func TestBrokerPassesListenOverride(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	code := a.brokerCmd(context.Background(), []string{"--listen", "127.0.0.1:17444"})
	if code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	got := stub.brokerCalls[0]
	if got.Listen != "127.0.0.1:17444" {
		t.Errorf("Listen = %q, want %q", got.Listen, "127.0.0.1:17444")
	}
	if got.Version != a.version {
		t.Errorf("Version = %q, want %q", got.Version, a.version)
	}
}

func TestBrokerWithoutListenLeavesConfigInCharge(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), nil); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if got := stub.brokerCalls[0].Listen; got != "" {
		t.Errorf("Listen = %q, want empty", got)
	}
}

func TestBrokerRejectsEmptyListen(t *testing.T) {
	for _, args := range [][]string{{"--listen", ""}, {"--listen="}} {
		stub := &stubDeps{}
		a, _, stderr := newBrokerCmdApp(stub)

		if code := a.brokerCmd(context.Background(), args); code != exitUsage {
			t.Fatalf("brokerCmd(%q) returned %d, want %d", args, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "--listen requires a non-empty address") {
			t.Errorf("stderr = %q, want the empty --listen explanation", stderr.String())
		}
		if len(stub.brokerCalls) != 0 {
			t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
		}
	}
}

// TestBrokerStartupFailureExitCodes covers REQ-broker-config-013 and
// REQ-broker-config-024: each startup failure keeps its own exit code and the
// original reason text reaches standard error.
func TestBrokerStartupFailureExitCodes(t *testing.T) {
	cases := []struct {
		failure startFailure
		code    int
	}{
		{failAlreadyRunning, exitAlreadyRunning},
		{failListenUnavailable, exitListenUnavailable},
		{failInvalidConfig, exitInvalidConfig},
		{failInSession, exitInSession},
		{failNotRoot, exitNotRoot},
		{failRuntimeDir, exitRuntimeDir},
		{failInvalidListen, exitUsage},
		{failOther, exitFailure},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("failure_%d", tc.failure), func(t *testing.T) {
			stub := &stubDeps{
				brokerErr:      errors.New("startup failed"),
				classifyResult: tc.failure,
			}
			a, _, stderr := newBrokerCmdApp(stub)

			if code := a.brokerCmd(context.Background(), nil); code != tc.code {
				t.Fatalf("brokerCmd returned %d, want %d", code, tc.code)
			}
			if want := "peers broker: startup failed"; brokerLastLine(stderr.String()) != want {
				t.Errorf("last stderr line = %q, want %q", brokerLastLine(stderr.String()), want)
			}
		})
	}
}

func TestBrokerPassesSystemFlag(t *testing.T) {
	stub := &stubDeps{
		brokerErr:      errors.New("startup failed"),
		classifyResult: failOther,
	}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"--system"}); code != exitFailure {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitFailure)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if !stub.brokerCalls[0].System {
		t.Error("System = false, want true")
	}
}

func TestBrokerPassesAllowDegraded(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"--allow-degraded"}); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if !stub.brokerCalls[0].AllowDegraded {
		t.Error("AllowDegraded = false, want true")
	}
}

func TestBrokerHelpMentionsAllowDegraded(t *testing.T) {
	stub := &stubDeps{}
	a, stdout, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"-h"}); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "--allow-degraded") {
		t.Errorf("usage = %q, want it to mention --allow-degraded", stdout.String())
	}
}

func TestBrokerRejectsPositionalArgument(t *testing.T) {
	stub := &stubDeps{}
	a, _, stderr := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"extra"}); code != exitUsage {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unexpected argument "extra"`) {
		t.Errorf("stderr = %q, want the unexpected argument message", stderr.String())
	}
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}

func TestBrokerHelpGoesToStdout(t *testing.T) {
	stub := &stubDeps{}
	a, stdout, stderr := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"-h"}); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "usage: peers broker") {
		t.Errorf("stdout = %q, want the broker usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}
