//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// termAdminCallWait bounds one terminal command and its Broker round trip.
	// The e2e wait convention (test/e2e/harness/wait.go) allows three seconds
	// unless a product timer sets the bound.
	termAdminCallWait = 3 * time.Second

	// linkDialTimeout and linkReconnectMin mirror the defaults of
	// link.dial_timeout and link.reconnect_min (internal/link/settings.go:88-96,
	// internal/config/keys.go:218-225). The system Broker configuration sets
	// neither.
	linkDialTimeout  = 5 * time.Second
	linkReconnectMin = time.Second

	// termAdminLinkWait bounds the wait for a paired link to reach connected.
	// It covers two dial attempts with one reconnect backoff between them: the
	// first dial can take link.dial_timeout, the loop then waits
	// link.reconnect_min, and the second dial can take link.dial_timeout again.
	// 2*5s + 1s = 11s.
	termAdminLinkWait = 2*linkDialTimeout + linkReconnectMin

	// termAdminSystemConfig is the system Broker's configuration file. A refused
	// config set must name this path.
	termAdminSystemConfig = "/etc/agent-peer/config.yaml"

	// termAdminPeerListen is the user-level Broker's listen address. It stands
	// in for the other host's Broker, so it must not collide with the system
	// Broker's default :17443.
	termAdminPeerListen = ":17444"

	// termAdminConfigKey is a configuration key the test reads and changes. It
	// affects neither the pairing nor the aliases used later.
	termAdminConfigKey = "directory.max_records"
	termAdminConfigOld = "1000"
	termAdminConfigNew = "900"

	// termAdminDeniedConfigValue is the value the account without write
	// permission asks for. The refusal must happen before it is applied.
	termAdminDeniedConfigValue = "800"
)

// TestREQBrokerConfig039_TerminalAdminCommands runs the REQ-broker-config-039
// acceptance on a system-level Broker: a session without write permission is
// refused a pairing key, root creates the key and reads and changes a
// configuration item in the terminal, a second Broker is paired from its own
// terminal as peer, both terminals list it connected, and the audit log records
// the operations in order under the account that ran them.
//
// The suite runs one container per test, so the second host is another Broker
// in the same container: peer's user-level Broker. The terminal on that host
// runs as peer and selects its Broker through AGENT_PEER_TEST_SOCKET; a Broker
// socket may serve only its own account or root, so root cannot drive another
// account's Broker.
func TestREQBrokerConfig039_TerminalAdminCommands(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	// The system Broker is host A. Peer's user-level Broker is host B. The
	// system network has no default route, so the Broker derives no advertised
	// address from its wildcard listen; name the loopback address explicitly so
	// pairing-key create prints one that co-located host B can dial.
	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n  advertise: \"127.0.0.1:17443\"\n")
	syscontainer.StartUserBroker(t, "peer", "--listen", termAdminPeerListen)
	hostBEnv := []string{"AGENT_PEER_TEST_SOCKET=" + syscontainer.UserSocketPath(t, "peer")}

	// An account without write permission on the system configuration file is
	// refused when it asks for a pairing key from its session.
	other := syscontainer.NewSession(t, "other", "no-write")
	termAdminWaitRegistered(t, other)
	text, isError := other.Call(t, "create_pairing_key", map[string]any{})
	if !isError {
		t.Fatalf("create_pairing_key as other without write permission succeeded: %s", text)
	}
	termAdminRequireDenied(t, "create_pairing_key as other", text)

	// Root creates the key in host A's terminal. The output must carry the key,
	// its expiry and at least one address the other host can dial.
	created := syscontainer.RunAs(t, "root", nil, "pairing-key", "create")
	termAdminRequireOK(t, created, "agent-peer pairing-key create as root")
	key, address := termAdminPairingKey(t, created.Stdout)

	// Neither side lists a peer yet, which also checks the empty listing.
	termAdminRequireNoBrokers(t, "root", nil, "host A")
	termAdminRequireNoBrokers(t, "peer", hostBEnv, "host B")

	// Peer on host B pairs with host A using only the printed information.
	connected := syscontainer.RunAs(t, "peer", hostBEnv, "peer", "connect", address, "--key", key)
	termAdminRequireOK(t, connected, "agent-peer peer connect as peer on the other host")
	pairedAlias, pairedFingerprint := termAdminConnectOutput(t, connected.Stdout)

	// Both terminals list the other Broker as a connected peer. Host A accepted
	// the pairing; host B dialed it.
	hostAPeer := termAdminWaitPeer(t, "root", nil, "host A", "acceptor")
	hostBPeer := termAdminWaitPeer(t, "peer", hostBEnv, "host B", "dialer")
	if hostBPeer.alias != pairedAlias || hostBPeer.fingerprint != pairedFingerprint {
		t.Errorf("host B lists peer %q (%s), want the paired alias %q and fingerprint %s",
			hostBPeer.alias, hostBPeer.fingerprint, pairedAlias, pairedFingerprint)
	}

	// Root reads and changes one configuration item of host A.
	got := syscontainer.RunAs(t, "root", nil, "config", "get", termAdminConfigKey)
	termAdminRequireOK(t, got, "agent-peer config get as root")
	wantGet := termAdminConfigKey + " = " + termAdminConfigOld
	if !strings.Contains(got.Stdout, wantGet) {
		t.Fatalf("config get does not print %q:\n%s", wantGet, got.Stdout)
	}

	set := syscontainer.RunAs(t, "root", nil, "config", "set", termAdminConfigKey, termAdminConfigNew)
	termAdminRequireOK(t, set, "agent-peer config set as root")
	wantSet := termAdminConfigKey + ": " + termAdminConfigOld + " -> " + termAdminConfigNew
	if got := strings.TrimSpace(set.Stdout); got != wantSet {
		t.Fatalf("config set printed %q, want %q", got, wantSet)
	}

	gotAfter := syscontainer.RunAs(t, "root", nil, "config", "get", termAdminConfigKey)
	termAdminRequireOK(t, gotAfter, "agent-peer config get as root after the change")
	wantGetAfter := termAdminConfigKey + " = " + termAdminConfigNew
	if !strings.Contains(gotAfter.Stdout, wantGetAfter) {
		t.Fatalf("config get does not print %q after the change:\n%s", wantGetAfter, gotAfter.Stdout)
	}

	// Root unpairs host B from host A.
	disconnected := syscontainer.RunAs(t, "root", nil, "peer", "disconnect", hostAPeer.alias)
	termAdminRequireOK(t, disconnected, "agent-peer peer disconnect as root")
	wantDisconnect := "Disconnected " + hostAPeer.alias
	if got := strings.TrimSpace(disconnected.Stdout); got != wantDisconnect {
		t.Fatalf("peer disconnect printed %q, want %q", got, wantDisconnect)
	}

	// Host A's audit holds the operations in the order they happened and under
	// the account that ran them. pairing_succeeded on this side is written by
	// the accepting side, which has no executing account. The match is an
	// ordered subsequence: link and file-watch records may interleave.
	termAdminWaitAudit(t, "root", nil, "host A", []termAdminEvent{
		{event: "pairing_key_created", account: "root"},
		{event: "pairing_succeeded"},
		{event: "config_set", account: "root"},
		{event: "unpaired", account: "root"},
	})
	// Host B initiated the pairing, so its audit names peer, the account that
	// ran the terminal command.
	termAdminWaitAudit(t, "peer", hostBEnv, "host B", []termAdminEvent{
		{event: "pairing_succeeded", account: "peer"},
	})

	// The account without write permission is refused a configuration change,
	// and the error names the configuration file.
	denied := syscontainer.RunAs(t, "other", nil, "config", "set", termAdminConfigKey, termAdminDeniedConfigValue)
	if denied.ExitCode != 1 {
		t.Fatalf("config set as other without write permission exited %d, want 1\nstdout:\n%s\nstderr:\n%s",
			denied.ExitCode, denied.Stdout, denied.Stderr)
	}
	termAdminRequireDenied(t, "config set as other", denied.Stdout+"\n"+denied.Stderr)
	if !strings.Contains(denied.Stderr, termAdminSystemConfig) {
		t.Errorf("config set as other does not name the configuration file %s:\n%s",
			termAdminSystemConfig, denied.Stderr)
	}

	// The refusal happened before the value was applied: root still reads the
	// value set earlier.
	afterDenied := syscontainer.RunAs(t, "root", nil, "config", "get", termAdminConfigKey)
	termAdminRequireOK(t, afterDenied, "agent-peer config get after the refused change")
	if want := termAdminConfigKey + " = " + termAdminConfigNew; !strings.Contains(afterDenied.Stdout, want) {
		t.Fatalf("config get after the refused change does not print %q:\n%s", want, afterDenied.Stdout)
	}

	// Neither refused operation may reach the audit log as an event by other.
	audit := syscontainer.RunAs(t, "root", nil, "audit")
	termAdminRequireOK(t, audit, "agent-peer audit as root")
	for _, event := range []string{"pairing_key_created", "config_set"} {
		if termAdminHasAuditEvent(audit.Stdout, event, "other") {
			t.Errorf("host A's audit records %s by other, but the call was refused:\n%s", event, audit.Stdout)
		}
	}
}

// termAdminEvent is one expected audit record. An empty account matches a
// record written without an executing account.
type termAdminEvent struct {
	event   string
	account string
}

// termAdminPeerRow is one row of "agent-peer peer list".
type termAdminPeerRow struct {
	alias       string
	fingerprint string
	peer        string
	status      string
	direction   string
}

// termAdminRequireOK fails the test unless the command exited 0.
func termAdminRequireOK(t *testing.T, res syscontainer.Result, what string) {
	t.Helper()
	if res.ExitCode != 0 {
		t.Fatalf("%s: exit %d\nstdout:\n%s\nstderr:\n%s",
			what, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// termAdminRequireDenied fails the test unless the Broker's refusal is the
// permission_denied error.
func termAdminRequireDenied(t *testing.T, what, text string) {
	t.Helper()
	if !strings.Contains(text, "permission_denied") {
		t.Fatalf("%s is not reported as permission_denied: %s", what, text)
	}
}

// termAdminWaitRegistered waits until the session's MCP server has registered
// with its Broker.
func termAdminWaitRegistered(t *testing.T, session *syscontainer.Session) {
	t.Helper()
	harness.Eventually(t, termAdminCallWait, "the session of "+session.Account+" to register", func() (bool, string) {
		text, isError := session.Call(t, "list_agents", map[string]any{})
		return !isError, text
	})
}

// termAdminPairingKey checks the output of "pairing-key create" and returns the
// key with the first address to dial.
func termAdminPairingKey(t *testing.T, stdout string) (key, address string) {
	t.Helper()

	var expires string
	inAddresses := false
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Key:"):
			key = strings.TrimSpace(strings.TrimPrefix(trimmed, "Key:"))
		case strings.HasPrefix(trimmed, "Expires:"):
			expires = strings.TrimSpace(strings.TrimPrefix(trimmed, "Expires:"))
		case trimmed == "Addresses:":
			inAddresses = true
		case inAddresses && trimmed != "":
			if address == "" {
				address = trimmed
			}
		}
	}

	if key == "" {
		t.Fatalf("pairing-key create printed no key:\n%s", stdout)
	}
	if expires == "" {
		t.Fatalf("pairing-key create printed no expiry:\n%s", stdout)
	}
	expiry, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		t.Fatalf("pairing-key create printed an expiry %q that is not RFC 3339: %v", expires, err)
	}
	if !strings.HasSuffix(expires, "Z") {
		t.Errorf("pairing-key create printed the expiry %q, want UTC with a Z suffix", expires)
	}
	if !expiry.After(time.Now()) {
		t.Errorf("pairing-key create printed the expiry %q, which is not in the future", expires)
	}
	if address == "" {
		t.Fatalf("pairing-key create printed no address:\n%s", stdout)
	}
	return key, address
}

// termAdminRequireNoBrokers checks that "peer list" reports an empty listing.
func termAdminRequireNoBrokers(t *testing.T, account string, env []string, host string) {
	t.Helper()
	res := syscontainer.RunAs(t, account, env, "peer", "list")
	termAdminRequireOK(t, res, "agent-peer peer list as "+account+" on "+host)
	if !strings.Contains(res.Stdout, "Brokers: none") {
		t.Fatalf("%s's peer list does not print \"Brokers: none\" before pairing:\n%s", host, res.Stdout)
	}
}

// termAdminConnectOutput checks the output of "peer connect" and returns the
// alias and fingerprint it names.
func termAdminConnectOutput(t *testing.T, stdout string) (alias, fingerprint string) {
	t.Helper()

	const prefix = "Paired with "
	line := strings.TrimSpace(stdout)
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("peer connect printed %q, want %q<alias> (<fingerprint>)", line, prefix)
	}
	rest := strings.TrimPrefix(line, prefix)
	open := strings.LastIndex(rest, " (")
	if open < 0 || !strings.HasSuffix(rest, ")") {
		t.Fatalf("peer connect printed %q, want %q<alias> (<fingerprint>)", line, prefix)
	}
	alias = strings.TrimSpace(rest[:open])
	fingerprint = rest[open+2 : len(rest)-1]
	if alias == "" {
		t.Fatalf("peer connect printed no alias: %q", line)
	}
	if !termAdminIsFingerprint(fingerprint) {
		t.Fatalf("peer connect printed fingerprint %q, want 64 lowercase hexadecimal digits", fingerprint)
	}
	return alias, fingerprint
}

// termAdminWaitPeer waits until "peer list" on one host shows the other Broker
// connected in wantDirection and returns that row.
func termAdminWaitPeer(t *testing.T, account string, env []string, host, wantDirection string) termAdminPeerRow {
	t.Helper()

	var row termAdminPeerRow
	harness.Eventually(t, termAdminLinkWait, host+"'s peer list to show the other Broker connected", func() (bool, string) {
		res := syscontainer.RunAs(t, account, env, "peer", "list")
		if res.ExitCode != 0 {
			return false, fmt.Sprintf("exit %d: %s", res.ExitCode, res.Stderr)
		}
		found, ok := termAdminConnectedPeer(res.Stdout, wantDirection)
		if !ok {
			return false, res.Stdout
		}
		row = found
		return true, ""
	})
	return row
}

// termAdminConnectedPeer returns the first row that names a connected peer in
// direction.
func termAdminConnectedPeer(stdout, direction string) (termAdminPeerRow, bool) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		// ALIAS, FINGERPRINT, PEER, STATUS, DIRECTION, then the addresses.
		if len(fields) < 6 || fields[0] == "ALIAS" {
			continue
		}
		row := termAdminPeerRow{
			alias:       fields[0],
			fingerprint: fields[1],
			peer:        fields[2],
			status:      fields[3],
			direction:   fields[4],
		}
		if row.peer == "yes" && row.status == "connected" && row.direction == direction {
			return row, true
		}
	}
	return termAdminPeerRow{}, false
}

// termAdminWaitAudit waits until the audit command's output holds wants as an
// ordered subsequence.
func termAdminWaitAudit(t *testing.T, account string, env []string, host string, wants []termAdminEvent) {
	t.Helper()

	harness.Eventually(t, termAdminCallWait, host+"'s audit log to record "+termAdminEventNames(wants)+" in order",
		func() (bool, string) {
			res := syscontainer.RunAs(t, account, env, "audit")
			if res.ExitCode != 0 {
				return false, fmt.Sprintf("exit %d: %s", res.ExitCode, res.Stderr)
			}
			return termAdminContainsOrder(res.Stdout, wants), res.Stdout
		})
}

// termAdminEventNames renders the wanted events for a failure message.
func termAdminEventNames(wants []termAdminEvent) string {
	names := make([]string, 0, len(wants))
	for _, want := range wants {
		name := want.event
		if want.account != "" {
			name += " by " + want.account
		}
		names = append(names, name)
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// termAdminContainsOrder reports whether the audit output holds wants as an
// ordered subsequence.
func termAdminContainsOrder(stdout string, wants []termAdminEvent) bool {
	next := 0
	for _, line := range strings.Split(stdout, "\n") {
		if next == len(wants) {
			break
		}
		event, account := termAdminAuditEvent(line)
		if event == wants[next].event && account == wants[next].account {
			next++
		}
	}
	return next == len(wants)
}

// termAdminHasAuditEvent reports whether the audit output holds event by
// account.
func termAdminHasAuditEvent(stdout, event, account string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		gotEvent, gotAccount := termAdminAuditEvent(line)
		if gotEvent == event && gotAccount == account {
			return true
		}
	}
	return false
}

// termAdminAuditEvent returns the event name and the account of one audit line.
// The line is the time, the event name, then key=value fields.
func termAdminAuditEvent(line string) (event, account string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", ""
	}
	event = fields[1]
	for _, field := range fields[2:] {
		if strings.HasPrefix(field, "account=") {
			account = strings.Trim(strings.TrimPrefix(field, "account="), "\"")
		}
	}
	return event, account
}

// termAdminIsFingerprint reports whether s is 64 lowercase hexadecimal digits.
func termAdminIsFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
