package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/mcpserver/termcmd"
)

// configGetCall records one runConfigGet call.
type configGetCall struct {
	key string
	out io.Writer
}

// configSetCall records one runConfigSet call.
type configSetCall struct {
	key   string
	value string
	out   io.Writer
}

// peerConnectCall records one runPeerConnect call.
type peerConnectCall struct {
	address string
	key     string
	alias   string
	out     io.Writer
	warn    io.Writer
}

// termcmdStub records the calls the terminal commands make into deps.
type termcmdStub struct {
	statusCalls int
	statusOut   io.Writer
	statusErr   error

	auditCalls int
	auditOut   io.Writer
	auditLimit uint32
	auditErr   error

	configGetCalls      []configGetCall
	configGetErr        error
	configSetCalls      []configSetCall
	configSetErr        error
	setOnlineCalls      []setOnlineCall
	setOnlineErr        error
	pairingKeyCalls     []io.Writer
	pairingKeyErr       error
	peerListCalls       []io.Writer
	peerListErr         error
	peerConnectCalls    []peerConnectCall
	peerConnectWarnings []string
	peerConnectErr      error
	peerDisconnectCalls []string
	peerDisconnectOut   []io.Writer
	peerDisconnectErr   error
}

func (s *termcmdStub) deps() deps {
	return deps{
		runStatus: func(_ context.Context, stdout io.Writer) error {
			s.statusCalls++
			s.statusOut = stdout
			return s.statusErr
		},
		runAudit: func(_ context.Context, stdout io.Writer, limit uint32) error {
			s.auditCalls++
			s.auditOut = stdout
			s.auditLimit = limit
			return s.auditErr
		},
		runConfigGet: func(_ context.Context, key string, stdout io.Writer) error {
			s.configGetCalls = append(s.configGetCalls, configGetCall{key: key, out: stdout})
			return s.configGetErr
		},
		runConfigSet: func(_ context.Context, key, value string, stdout io.Writer) error {
			s.configSetCalls = append(s.configSetCalls, configSetCall{key: key, value: value, out: stdout})
			return s.configSetErr
		},
		runSetOnline: func(_ context.Context, online bool, stdout io.Writer) error {
			s.setOnlineCalls = append(s.setOnlineCalls, setOnlineCall{online: online, out: stdout})
			return s.setOnlineErr
		},
		runPairingKeyCreate: func(_ context.Context, stdout io.Writer) error {
			s.pairingKeyCalls = append(s.pairingKeyCalls, stdout)
			return s.pairingKeyErr
		},
		runPeerList: func(_ context.Context, stdout io.Writer) error {
			s.peerListCalls = append(s.peerListCalls, stdout)
			return s.peerListErr
		},
		runPeerConnect: func(_ context.Context, address, key, alias string, stdout, warn io.Writer) error {
			s.peerConnectCalls = append(s.peerConnectCalls, peerConnectCall{
				address: address, key: key, alias: alias, out: stdout, warn: warn,
			})
			for _, warning := range s.peerConnectWarnings {
				_, _ = fmt.Fprintf(warn, "warning: %s\n", warning)
			}
			return s.peerConnectErr
		},
		runPeerDisconnect: func(_ context.Context, broker string, stdout io.Writer) error {
			s.peerDisconnectCalls = append(s.peerDisconnectCalls, broker)
			s.peerDisconnectOut = append(s.peerDisconnectOut, stdout)
			return s.peerDisconnectErr
		},
	}
}

func newTermcmdApp(s *termcmdStub) (*app, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &app{stdout: stdout, stderr: stderr, deps: s.deps()}, stdout, stderr
}

func TestStatusCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.statusCmd(context.Background(), nil); got != exitOK {
		t.Fatalf("statusCmd returned %d, want %d", got, exitOK)
	}
	if stub.statusCalls != 1 {
		t.Fatalf("runStatus called %d times, want 1", stub.statusCalls)
	}
	if stub.statusOut != stdout {
		t.Fatalf("runStatus got writer %T, want app.stdout", stub.statusOut)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestStatusErrorExitsOne(t *testing.T) {
	stub := &termcmdStub{statusErr: errors.New("permission_denied: x")}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.statusCmd(context.Background(), nil); got != exitFailure {
		t.Fatalf("statusCmd returned %d, want %d", got, exitFailure)
	}
	if want := "peers status: permission_denied: x\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestStatusDiagnosticsOnlyExitsOne checks that the status command fails
// without adding a line: the status output already names the cause and repair.
func TestStatusDiagnosticsOnlyExitsOne(t *testing.T) {
	stub := &termcmdStub{statusErr: termcmd.ErrDiagnosticsOnly}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.statusCmd(context.Background(), nil); got != exitFailure {
		t.Fatalf("statusCmd returned %d, want %d", got, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty; the runner wrote the status itself", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

// TestStatusRejectsUnknownFlag checks the usage-error contract on the command
// line: the usage comes first and the last line is "peers status: <reason>".
func TestStatusRejectsUnknownFlag(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.statusCmd(context.Background(), []string{"--bogus"}); got != exitUsage {
		t.Fatalf("statusCmd returned %d, want %d", got, exitUsage)
	}
	if stub.statusCalls != 0 {
		t.Fatalf("runStatus called %d times, want 0", stub.statusCalls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), statusUsage) {
		t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
	}
	if got, want := brokerLastLine(stderr.String()), "peers status: flag provided but not defined: -bogus"; got != want {
		t.Fatalf("last stderr line = %q, want %q", got, want)
	}
}

func TestStatusRejectsArguments(t *testing.T) {
	stub := &termcmdStub{}
	a, _, stderr := newTermcmdApp(stub)

	if got := a.statusCmd(context.Background(), []string{"x"}); got != exitUsage {
		t.Fatalf("statusCmd returned %d, want %d", got, exitUsage)
	}
	if stub.statusCalls != 0 {
		t.Fatalf("runStatus called %d times, want 0", stub.statusCalls)
	}
	if !strings.HasPrefix(stderr.String(), statusUsage) {
		t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
	}
	if got, want := brokerLastLine(stderr.String()), `peers status: unexpected argument "x"`; got != want {
		t.Fatalf("last stderr line = %q, want %q", got, want)
	}
}

func TestAuditRejectsArguments(t *testing.T) {
	stub := &termcmdStub{}
	a, _, stderr := newTermcmdApp(stub)

	if got := a.auditCmd(context.Background(), []string{"x"}); got != exitUsage {
		t.Fatalf("auditCmd returned %d, want %d", got, exitUsage)
	}
	if stub.auditCalls != 0 {
		t.Fatalf("runAudit called %d times, want 0", stub.auditCalls)
	}
	if !strings.HasPrefix(stderr.String(), auditUsage) {
		t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
	}
	if got, want := brokerLastLine(stderr.String()), `peers audit: unexpected argument "x"`; got != want {
		t.Fatalf("last stderr line = %q, want %q", got, want)
	}
}

func TestAuditLimit(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want uint32
	}{
		{"default is unlimited", nil, 0},
		{"separate value", []string{"--limit", "5"}, 5},
		{"equals zero", []string{"--limit=0"}, 0},
		{"single dash", []string{"-limit", "7"}, 7},
		{"leading zero is decimal", []string{"--limit", "010"}, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, _ := newTermcmdApp(stub)

			if got := a.auditCmd(context.Background(), tc.args); got != exitOK {
				t.Fatalf("auditCmd returned %d, want %d", got, exitOK)
			}
			if stub.auditCalls != 1 {
				t.Fatalf("runAudit called %d times, want 1", stub.auditCalls)
			}
			if stub.auditLimit != tc.want {
				t.Fatalf("runAudit got limit %d, want %d", stub.auditLimit, tc.want)
			}
		})
	}
}

func TestAuditLimitRange(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"above uint32", []string{"--limit", "4294967296"}},
		{"negative", []string{"--limit", "-1"}},
		{"not a number", []string{"--limit", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, stderr := newTermcmdApp(stub)

			if got := a.auditCmd(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("auditCmd returned %d, want %d", got, exitUsage)
			}
			if stub.auditCalls != 0 {
				t.Fatalf("runAudit called %d times, want 0", stub.auditCalls)
			}
			if !strings.HasPrefix(stderr.String(), auditUsage) {
				t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
			}
			if got := brokerLastLine(stderr.String()); !strings.HasPrefix(got, "peers audit: ") {
				t.Fatalf("last stderr line = %q, want it to start with %q", got, "peers audit: ")
			}
		})
	}
}

func TestAuditErrorExitsOne(t *testing.T) {
	stub := &termcmdStub{auditErr: errors.New("permission_denied: x")}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.auditCmd(context.Background(), nil); got != exitFailure {
		t.Fatalf("auditCmd returned %d, want %d", got, exitFailure)
	}
	if want := "peers audit: permission_denied: x\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
