package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// adminCalls counts every dependency call a stub recorded.
func adminCalls(s *termcmdStub) int {
	return s.statusCalls + s.auditCalls +
		len(s.configGetCalls) + len(s.configSetCalls) +
		len(s.pairingKeyCalls) + len(s.peerListCalls) +
		len(s.peerConnectCalls) + len(s.peerDisconnectCalls)
}

func TestAdminCommandsRegistered(t *testing.T) {
	for _, name := range []string{"config", "pairing-key", "peer"} {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("subcommands[%q] is missing", name)
		}
	}
}

func TestTopLevelUsageListsAdminCommands(t *testing.T) {
	a, stdout, stderr := newTermcmdApp(&termcmdStub{})

	if got := a.run(context.Background(), []string{"--help"}); got != exitOK {
		t.Fatalf("run(--help) = %d, want %d", got, exitOK)
	}
	for _, want := range []string{
		"config get", "config set", "pairing-key create",
		"peer list", "peer connect", "peer disconnect",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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

func TestPairingKeyCreateCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"pairing-key", "create"}); got != exitOK {
		t.Fatalf("run(pairing-key create) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.pairingKeyCalls) != 1 {
		t.Fatalf("runPairingKey called %d times, want 1", len(stub.pairingKeyCalls))
	}
	if stub.pairingKeyCalls[0] != stdout {
		t.Fatalf("runPairingKey got writer %T, want app.stdout", stub.pairingKeyCalls[0])
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
}

func TestPeerListCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"peer", "list"}); got != exitOK {
		t.Fatalf("run(peer list) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.peerListCalls) != 1 {
		t.Fatalf("runPeerList called %d times, want 1", len(stub.peerListCalls))
	}
	if stub.peerListCalls[0] != stdout {
		t.Fatalf("runPeerList got writer %T, want app.stdout", stub.peerListCalls[0])
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
}

func TestPeerConnectCallsRunner(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want peerConnectCall
	}{
		{
			name: "address before flags",
			args: []string{"peer", "connect", "peer.example:17443", "--key", "secret"},
			want: peerConnectCall{address: "peer.example:17443", key: "secret"},
		},
		{
			name: "flags before address",
			args: []string{"peer", "connect", "--key", "secret", "peer.example:17443"},
			want: peerConnectCall{address: "peer.example:17443", key: "secret"},
		},
		{
			name: "equals forms",
			args: []string{"peer", "connect", "peer.example:17443", "--key=secret", "--alias=web"},
			want: peerConnectCall{address: "peer.example:17443", key: "secret", alias: "web"},
		},
		{
			name: "alias after address",
			args: []string{"peer", "connect", "peer.example:17443", "--key", "secret", "--alias", "web"},
			want: peerConnectCall{address: "peer.example:17443", key: "secret", alias: "web"},
		},
		{
			name: "hostname form",
			args: []string{"peer", "connect", "web(peer.example:17443)", "--key=secret"},
			want: peerConnectCall{address: "web(peer.example:17443)", key: "secret"},
		},
		{
			name: "single dash flags",
			args: []string{"peer", "connect", "-key", "secret", "-alias", "web", "peer.example:17443"},
			want: peerConnectCall{address: "peer.example:17443", key: "secret", alias: "web"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitOK, stderr.String())
			}
			if len(stub.peerConnectCalls) != 1 {
				t.Fatalf("runPeerConnect called %d times, want 1", len(stub.peerConnectCalls))
			}
			got := stub.peerConnectCalls[0]
			if got.address != tc.want.address || got.key != tc.want.key || got.alias != tc.want.alias {
				t.Fatalf("runPeerConnect got (%q, %q, %q), want (%q, %q, %q)",
					got.address, got.key, got.alias, tc.want.address, tc.want.key, tc.want.alias)
			}
			if got.out != stdout {
				t.Fatalf("runPeerConnect got writer %T, want app.stdout", got.out)
			}
			if got.warn != stderr {
				t.Fatalf("runPeerConnect got warn %T, want app.stderr", got.warn)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
			}
		})
	}
}

// TestPeerConnectWarningsReachStderr pins the warning contract on the command
// side: whatever the termcmd call writes to its warn writer lands on standard
// error with a "warning: " prefix, and a successful connect still exits 0.
func TestPeerConnectWarningsReachStderr(t *testing.T) {
	stub := &termcmdStub{peerConnectWarnings: []string{"peer is already paired"}}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"peer", "connect", "peer.example:17443", "--key", "secret"}); got != exitOK {
		t.Fatalf("run(peer connect) = %d, want %d", got, exitOK)
	}
	if want := "warning: peer is already paired\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestPeerDisconnectCallsRunner(t *testing.T) {
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"peer", "disconnect", "web"}); got != exitOK {
		t.Fatalf("run(peer disconnect) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.peerDisconnectCalls) != 1 {
		t.Fatalf("runPeerDisconnect called %d times, want 1", len(stub.peerDisconnectCalls))
	}
	if stub.peerDisconnectCalls[0] != "web" {
		t.Fatalf("runPeerDisconnect broker = %q, want web", stub.peerDisconnectCalls[0])
	}
	if stub.peerDisconnectOut[0] != stdout {
		t.Fatalf("runPeerDisconnect got writer %T, want app.stdout", stub.peerDisconnectOut[0])
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
			name: "config get", args: []string{"config", "get"}, command: "config get",
			prepare: func(s *termcmdStub) { s.configGetErr = errDenied },
		},
		{
			name: "config set", args: []string{"config", "set", "k", "v"}, command: "config set",
			prepare: func(s *termcmdStub) { s.configSetErr = errDenied },
		},
		{
			name: "pairing-key create", args: []string{"pairing-key", "create"}, command: "pairing-key create",
			prepare: func(s *termcmdStub) { s.pairingKeyErr = errDenied },
		},
		{
			name: "peer list", args: []string{"peer", "list"}, command: "peer list",
			prepare: func(s *termcmdStub) { s.peerListErr = errDenied },
		},
		{
			name: "peer connect", args: []string{"peer", "connect", "a:1", "--key", "k"}, command: "peer connect",
			prepare: func(s *termcmdStub) { s.peerConnectErr = errDenied },
		},
		{
			name: "peer disconnect", args: []string{"peer", "disconnect", "web"}, command: "peer disconnect",
			prepare: func(s *termcmdStub) { s.peerDisconnectErr = errDenied },
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
		{"pairing-key without verb", []string{"pairing-key"}, pairingKeyUsage, "pairing-key"},
		{"pairing-key unknown verb", []string{"pairing-key", "bogus"}, pairingKeyUsage, "pairing-key"},
		{"pairing-key create extra argument", []string{"pairing-key", "create", "x"}, pairingKeyUsage, "pairing-key create"},
		{"peer without verb", []string{"peer"}, peerUsage, "peer"},
		{"peer unknown verb", []string{"peer", "bogus"}, peerUsage, "peer"},
		{"peer list extra argument", []string{"peer", "list", "x"}, peerUsage, "peer list"},
		{"peer connect missing address", []string{"peer", "connect", "--key", "k"}, peerUsage, "peer connect"},
		{"peer connect missing key", []string{"peer", "connect", "a:1"}, peerUsage, "peer connect"},
		{"peer connect empty key", []string{"peer", "connect", "a:1", "--key="}, peerUsage, "peer connect"},
		{"peer connect two addresses", []string{"peer", "connect", "a", "b", "--key", "k"}, peerUsage, "peer connect"},
		{"peer connect unknown flag", []string{"peer", "connect", "a", "--key", "k", "--bogus"}, peerUsage, "peer connect"},
		{"peer connect missing key value", []string{"peer", "connect", "a", "--key"}, peerUsage, "peer connect"},
		{"peer connect missing alias value", []string{"peer", "connect", "a", "--key", "k", "--alias"}, peerUsage, "peer connect"},
		{"peer disconnect missing broker", []string{"peer", "disconnect"}, peerUsage, "peer disconnect"},
		{"peer disconnect extra argument", []string{"peer", "disconnect", "a", "b"}, peerUsage, "peer disconnect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, exitUsage)
			}
			if calls := adminCalls(stub); calls != 0 {
				t.Fatalf("dependencies called %d times, want 0", calls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), tc.usage) {
				t.Fatalf("stderr = %q, want the usage before the reason", stderr.String())
			}
			if last := brokerLastLine(stderr.String()); !strings.HasPrefix(last, "peers "+tc.command+": ") {
				t.Fatalf("last stderr line = %q, want it to start with %q", last, "peers "+tc.command+": ")
			}
		})
	}
}

// TestPeerConnectExtraArgumentIsNotEchoed covers the case where a user leaves
// out --key and the pairing key becomes the second positional argument. The
// usage error must not repeat it, because it is a secret.
func TestPeerConnectExtraArgumentIsNotEchoed(t *testing.T) {
	const key = "ABCD-EFGH-IJKL-MNOP"
	stub := &termcmdStub{}
	a, stdout, stderr := newTermcmdApp(stub)

	if got := a.run(context.Background(), []string{"peer", "connect", "a:1", key}); got != exitUsage {
		t.Fatalf("run(peer connect a:1 <key>) = %d, want %d", got, exitUsage)
	}
	if calls := adminCalls(stub); calls != 0 {
		t.Fatalf("dependencies called %d times, want 0", calls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if strings.Contains(stderr.String(), key) {
		t.Fatalf("stderr repeats the extra argument: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "position 2") {
		t.Fatalf("stderr = %q, want the position of the extra argument", stderr.String())
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
		{"pairing-key -h", []string{"pairing-key", "-h"}, pairingKeyUsage},
		{"pairing-key create --help", []string{"pairing-key", "create", "--help"}, pairingKeyUsage},
		{"pairing-key create after argument", []string{"pairing-key", "create", "x", "--help"}, pairingKeyUsage},
		{"peer -h", []string{"peer", "-h"}, peerUsage},
		{"peer list --help", []string{"peer", "list", "--help"}, peerUsage},
		{"peer list after argument", []string{"peer", "list", "x", "--help"}, peerUsage},
		{"peer connect --help", []string{"peer", "connect", "--help"}, peerUsage},
		{"peer connect after address", []string{"peer", "connect", "a:1", "--help"}, peerUsage},
		{"peer disconnect -h", []string{"peer", "disconnect", "-h"}, peerUsage},
		{"peer disconnect after arguments", []string{"peer", "disconnect", "a", "b", "--help"}, peerUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &termcmdStub{}
			a, stdout, stderr := newTermcmdApp(stub)

			if got := a.run(context.Background(), tc.args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %q)", tc.args, got, exitOK, stderr.String())
			}
			if calls := adminCalls(stub); calls != 0 {
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
