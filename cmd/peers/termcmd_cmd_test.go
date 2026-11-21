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
	statusCalls  int
	statusSystem bool
	statusOut    io.Writer
	statusErr    error

	auditCalls  int
	auditSystem bool
	auditOut    io.Writer
	auditLimit  uint32
	auditErr    error

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
	peerNames           []string
	peerNamesErr        error
	peerNamesCalls      int
}

func (s *termcmdStub) deps() deps {
	return deps{
		euid: func() int { return 1000 },
		runStatus: func(_ context.Context, system bool, stdout io.Writer) error {
			s.statusCalls++
			s.statusSystem = system
			s.statusOut = stdout
			return s.statusErr
		},
		runAudit: func(_ context.Context, system bool, stdout io.Writer, limit uint32) error {
			s.auditCalls++
			s.auditSystem = system
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
		runSetOnline: func(_ context.Context, system, online bool, stdout io.Writer) error {
			s.setOnlineCalls = append(s.setOnlineCalls, setOnlineCall{system: system, online: online, out: stdout})
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
		peerNames: func(context.Context) ([]string, error) {
			s.peerNamesCalls++
			return s.peerNames, s.peerNamesErr
		},
	}
}

func newTermcmdApp(s *termcmdStub) (*app, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &app{stdout: stdout, stderr: stderr, deps: s.deps()}, stdout, stderr
}

// runService runs "peers service" with args through the command tree.
func runService(a *app, args ...string) int {
	return a.run(context.Background(), append([]string{"service"}, args...))
}

func TestStatusCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := runService(a, "status"); got != exitOK {
		t.Fatalf("service status returned %d, want %d", got, exitOK)
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

	if got := runService(a, "status"); got != exitFailure {
		t.Fatalf("service status returned %d, want %d", got, exitFailure)
	}
	if want := "peers service status: permission_denied: x\n"; stderr.String() != want {
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

	if got := runService(a, "status"); got != exitFailure {
		t.Fatalf("service status returned %d, want %d", got, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty; the runner wrote the status itself", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

// TestStatusRejectsUnknownFlag checks the usage-error contract on the command
// line: a bad flag exits with the usage code before the runner is reached.
func TestStatusRejectsUnknownFlag(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := runService(a, "status", "--bogus"); got != exitUsage {
		t.Fatalf("service status returned %d, want %d", got, exitUsage)
	}
	if stub.statusCalls != 0 {
		t.Fatalf("runStatus called %d times, want 0", stub.statusCalls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if want := usageDiagnostic("peers service status", "unknown flag: --bogus"); stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestStatusRejectsArguments(t *testing.T) {
	stub := &termcmdStub{}
	a, _, stderr := newTermcmdApp(stub)

	if got := runService(a, "status", "x"); got != exitUsage {
		t.Fatalf("service status returned %d, want %d", got, exitUsage)
	}
	if stub.statusCalls != 0 {
		t.Fatalf("runStatus called %d times, want 0", stub.statusCalls)
	}
	if want := usageDiagnostic("peers service status", `unexpected argument "x"`); stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestAuditRejectsArguments(t *testing.T) {
	stub := &termcmdStub{}
	a, _, stderr := newTermcmdApp(stub)

	if got := runService(a, "audit", "x"); got != exitUsage {
		t.Fatalf("service audit returned %d, want %d", got, exitUsage)
	}
	if stub.auditCalls != 0 {
		t.Fatalf("runAudit called %d times, want 0", stub.auditCalls)
	}
	if want := usageDiagnostic("peers service audit", `unexpected argument "x"`); stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
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
		{"leading zero is decimal", []string{"--limit", "010"}, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, _ := newTermcmdApp(stub)

			if got := runService(a, append([]string{"audit"}, tc.args...)...); got != exitOK {
				t.Fatalf("service audit returned %d, want %d", got, exitOK)
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
		name   string
		args   []string
		prefix string
	}{
		{"above uint32", []string{"--limit", "4294967296"}, "peers service audit: "},
		{"negative", []string{"--limit", "-1"}, "peers service audit: "},
		{"not a number", []string{"--limit", "x"}, "peers service audit: "},
		// Long flags use two dashes; a single dash is not accepted, and the
		// flag parser rejects it before the command body runs.
		{"single dash", []string{"-limit", "7"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, stderr := newTermcmdApp(stub)

			if got := runService(a, append([]string{"audit"}, tc.args...)...); got != exitUsage {
				t.Fatalf("service audit returned %d, want %d", got, exitUsage)
			}
			if stub.auditCalls != 0 {
				t.Fatalf("runAudit called %d times, want 0", stub.auditCalls)
			}
			if tc.prefix != "" && !strings.HasPrefix(stderr.String(), tc.prefix) {
				t.Fatalf("stderr = %q, want it to start with %q", stderr.String(), tc.prefix)
			}
			if stderr.Len() == 0 {
				t.Fatal("stderr is empty, want the reason")
			}
		})
	}
}

func TestAuditErrorExitsOne(t *testing.T) {
	stub := &termcmdStub{auditErr: errors.New("permission_denied: x")}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := runService(a, "audit"); got != exitFailure {
		t.Fatalf("service audit returned %d, want %d", got, exitFailure)
	}
	if want := "peers service audit: permission_denied: x\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestServiceLevelFlagsAreMutuallyExclusive checks the shared level rule on the
// commands that act on the Broker: status and audit still reject the conflict
// before reaching the runner.
func TestServiceLevelFlagsAreMutuallyExclusive(t *testing.T) {
	for _, name := range []string{"status", "audit", "online", "offline"} {
		t.Run(name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, _, stderr := newTermcmdApp(stub)

			if got := runService(a, name, "--system", "--user"); got != exitUsage {
				t.Fatalf("service %s returned %d, want %d", name, got, exitUsage)
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") {
				t.Fatalf("stderr = %q, want the level-flag conflict", stderr.String())
			}
			if stub.statusCalls != 0 || stub.auditCalls != 0 || len(stub.setOnlineCalls) != 0 {
				t.Fatalf("a runner was called for a usage error")
			}
		})
	}
}
