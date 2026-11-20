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

	// termAdminPeerListen is host B's user-level Broker listen address. It must
	// not collide with host A's system Broker default :17443.
	termAdminPeerListen = ":17444"

	// termAdminConfigKey is a configuration key the test reads and changes. It
	// affects neither the pairing nor the aliases used later.
	termAdminConfigKey = "directory.max_records"
	termAdminConfigOld = "1000"
	termAdminConfigNew = "900"

	// termAdminDeniedConfigValue is the value the account without write
	// permission asks for. The refusal must happen before it is applied.
	termAdminDeniedConfigValue = "800"

	// termAdminAdvertise is host A's advertised address. The internal network
	// has no route out, so the Broker cannot derive a dialable address from its
	// wildcard listen; the name is the network alias host B resolves.
	termAdminAdvertise = "hostA:17443"

	// termAdminSharedDir is the path both pair containers mount the same named
	// volume at; it must match test/e2e/compose/system/compose.yaml.
	termAdminSharedDir = "/shared"

	// termAdminPairingFile carries host A's address and pairing key to host B.
	// termAdminConnectedFile announces that host B paired, and
	// termAdminDoneFile lets host B keep its Broker alive until host A has
	// finished the audit and disconnect assertions.
	termAdminPairingFile   = "pairing"
	termAdminConnectedFile = "connected"
	termAdminDoneFile      = "done"
)

// e2ePairOptions is the pair configuration TestMain and the test both pass to
// the harness.
var e2ePairOptions = syscontainer.PairOptions{SharedDir: termAdminSharedDir}

// TestREQBrokerConfig039_TerminalAdminCommands runs the REQ-broker-config-039
// acceptance on a system-level Broker: a session without write permission is
// refused a pairing key, root creates the key and reads and changes a
// configuration item in the terminal, a second host is paired from its own
// terminal as peer, both terminals list it connected, and the audit log records
// the operations in order under the account that ran them.
//
// The test runs on two containers that are separate hosts on one internal
// network. Host A runs the system Broker and accepts the pairing. Host B runs
// its own user-level Broker and dials host A using only the address and key
// host A printed. The two sides meet in the shared directory and keep their
// Brokers alive until the other side has finished; a failure on either side is
// recorded there so the other side fails instead of running out its wait.
func TestREQBrokerConfig039_TerminalAdminCommands(t *testing.T) {
	if !syscontainer.InContainer() {
		// The host side runs like the single-container cases: it starts its
		// own project and subnet, so it can overlap the other tests.
		t.Parallel()
		syscontainer.RunPairTest(t, e2ePairOptions)
		return
	}

	switch syscontainer.PairRole() {
	case syscontainer.RoleA:
		termAdminHostA(t)
	case syscontainer.RoleB:
		termAdminHostB(t)
	default:
		t.Fatalf("the pair test runs in a pair container as role A or B")
	}
}

// termAdminHostA runs the accepting side: the system Broker, the refused
// session, the terminal commands root performs, and host A's audit assertions.
func termAdminHostA(t *testing.T) {
	syscontainer.PairGuard(t)

	// Root creates the key in host A's terminal, then writes the address and
	// key into the shared directory for host B. The Broker names itself by its
	// network alias so the key prints an address host B can dial.
	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n  advertise: \""+termAdminAdvertise+"\"\n")

	// An account without write permission on the system configuration file is
	// refused when it asks for a pairing key from its session.
	other := syscontainer.NewSession(t, "other", "no-write")
	termAdminWaitRegistered(t, other)
	text, isError := other.Call(t, "create_pairing_key", map[string]any{})
	if !isError {
		t.Fatalf("create_pairing_key as other without write permission succeeded: %s", text)
	}
	termAdminRequireDenied(t, "create_pairing_key as other", text)

	// The output must carry the key, its expiry and the advertised address host
	// B can dial.
	created := syscontainer.RunAs(t, "root", nil, "pairing-key", "create")
	termAdminRequireOK(t, created, "agent-peer pairing-key create as root")
	key, shown := termAdminPairingKey(t, created.Stdout)
	address := termAdminAdvertisedAddress(t, shown)

	// Host A lists no peer yet, which also checks the empty listing. The check
	// runs before the key is published, so host B cannot have paired when it
	// runs.
	termAdminRequireNoBrokers(t, "root", nil, "host A")
	syscontainer.PairWrite(t, termAdminPairingFile, []byte(address+"\n"+key+"\n"))

	// Host B pairs using only that information and says so. Host A accepted the
	// pairing, so its row is the acceptor.
	syscontainer.PairAwait(t, termAdminConnectedFile)
	hostAPeer := termAdminWaitPeer(t, "root", nil, "host A", "acceptor")

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

	// The disconnect removed the pair, so host A lists no Broker again.
	termAdminRequireNoBrokers(t, "root", nil, "host A")

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

	// Host A has made every assertion that needs the link up. Host B may now
	// stop keeping its Broker alive.
	syscontainer.PairWrite(t, termAdminDoneFile, []byte("done\n"))
}

// termAdminHostB runs the dialing side: host B's own user-level Broker and the
// terminal command that pairs it with host A.
func termAdminHostB(t *testing.T) {
	syscontainer.PairGuard(t)

	syscontainer.StartUserBroker(t, "peer", "--listen", termAdminPeerListen)
	peerEnv := []string{"AGENT_PEER_TEST_SOCKET=" + syscontainer.UserSocketPath(t, "peer")}

	// Host B lists no peer yet, which also checks the empty listing.
	termAdminRequireNoBrokers(t, "peer", peerEnv, "host B")

	// Pair with host A using only the address and key host A printed.
	data := syscontainer.PairAwait(t, termAdminPairingFile)
	address, key := termAdminParsePairing(t, data)
	connected := syscontainer.RunAs(t, "peer", peerEnv, "peer", "connect", address, "--key", key)
	termAdminRequireOK(t, connected, "agent-peer peer connect as peer on the other host")
	pairedAlias, pairedFingerprint := termAdminConnectOutput(t, connected.Stdout)

	// Host B's terminal lists host A as a connected dialer peer, matching what
	// peer connect printed.
	hostBPeer := termAdminWaitPeer(t, "peer", peerEnv, "host B", "dialer")
	if hostBPeer.alias != pairedAlias || hostBPeer.fingerprint != pairedFingerprint {
		t.Errorf("host B lists peer %q (%s), want the paired alias %q and fingerprint %s",
			hostBPeer.alias, hostBPeer.fingerprint, pairedAlias, pairedFingerprint)
	}

	// Host B initiated the pairing, so its audit names peer, the account that
	// ran the terminal command.
	termAdminWaitAudit(t, "peer", peerEnv, "host B", []termAdminEvent{
		{event: "pairing_succeeded", account: "peer"},
	})
	syscontainer.PairWrite(t, termAdminConnectedFile, []byte("connected\n"))

	// Keep host B's Broker alive until host A has listed the peer and run the
	// disconnect, so the assertions there see the link still up.
	syscontainer.PairAwait(t, termAdminDoneFile)

	// Host A unpaired, and pairing drops the link when the removed entry is not
	// replaced. Host B may take a moment to observe the closed link, so wait for
	// its listing to stop showing a connected row.
	harness.Eventually(t, termAdminCallWait, "host B's peer list to stop showing the other Broker connected",
		func() (bool, string) {
			res := syscontainer.RunAs(t, "peer", peerEnv, "peer", "list")
			if res.ExitCode != 0 {
				return false, fmt.Sprintf("exit %d: %s", res.ExitCode, res.Stderr)
			}
			return !termAdminHasConnectedRow(res.Stdout), res.Stdout
		})
}

// termAdminParsePairing splits the shared pairing file into the address and
// key host A wrote.
func termAdminParsePairing(t *testing.T, data []byte) (address, key string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		t.Fatalf("the shared pairing file is %q, want an address line and a key line", data)
	}
	return lines[0], lines[1]
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
// key with every address it printed.
func termAdminPairingKey(t *testing.T, stdout string) (key string, shown []string) {
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
			shown = append(shown, trimmed)
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
	if len(shown) == 0 {
		t.Fatalf("pairing-key create printed no address:\n%s", stdout)
	}
	return key, shown
}

// termAdminAdvertisedAddress returns the printed address whose dial part is
// termAdminAdvertise. The command prints each address as "name(host:port)", and
// peer connect accepts that whole form. Any other printed address may be
// unreachable from the other host, so the configured one must be present.
func termAdminAdvertisedAddress(t *testing.T, shown []string) string {
	t.Helper()
	for _, entry := range shown {
		dial := entry
		if open := strings.LastIndex(entry, "("); open > 0 && strings.HasSuffix(entry, ")") {
			dial = entry[open+1 : len(entry)-1]
		}
		if dial == termAdminAdvertise {
			return entry
		}
	}
	t.Fatalf("pairing-key create printed addresses %v, none dials %s", shown, termAdminAdvertise)
	return ""
}

// termAdminRequireNoBrokers checks that "peer list" reports an empty listing.
func termAdminRequireNoBrokers(t *testing.T, account string, env []string, host string) {
	t.Helper()
	res := syscontainer.RunAs(t, account, env, "peer", "list")
	termAdminRequireOK(t, res, "agent-peer peer list as "+account+" on "+host)
	if !strings.Contains(res.Stdout, "Brokers: none") {
		t.Fatalf("%s's peer list does not print \"Brokers: none\":\n%s", host, res.Stdout)
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

// termAdminHasConnectedRow reports whether the listing holds any row whose
// STATUS is connected, whichever side dialed.
func termAdminHasConnectedRow(stdout string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		// ALIAS, FINGERPRINT, PEER, STATUS, DIRECTION, then the addresses.
		if len(fields) < 6 || fields[0] == "ALIAS" {
			continue
		}
		if fields[3] == "connected" {
			return true
		}
	}
	return false
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
