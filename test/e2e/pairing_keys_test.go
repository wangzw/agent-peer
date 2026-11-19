//go:build e2e

package e2e

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// pkWaitBound is the project-wide bound for a wait that no product timer
// governs. The plan's sum-of-timers rule therefore reduces to three seconds.
const pkWaitBound = 3 * time.Second

// pkRelinkBound is reconnect_max (400ms) + dial_timeout (500ms) + 3s, the
// bound the plan fixes for reconnecting under FastTimers.
const pkRelinkBound = 3900 * time.Millisecond

// pkStatic16 and pkStaticB are 16-character static keys, the shortest legal
// length. pkStatic128 is the longest legal length; pkStaticRuntimeBad is one
// character over it.
var (
	pkStatic16         = "static-key-00001"
	pkStaticB          = "static-key-00002"
	pkStaticReplace    = "static-key-00003"
	pkStatic128        = strings.Repeat("k", 128)
	pkStaticRuntimeBad = strings.Repeat("z", 129)
	pkStaticShort      = strings.Repeat("x", 15)
)

// TestPairingKeys covers one-time keys, the preset static key, wrong keys,
// an unpaired dial and the hot replacement of the static key on a flat
// topology. The minute-scale parts of the requirements are covered by the
// pairing unit tests named below; waiting ten minutes here would not exercise
// any additional production code.
func TestPairingKeys(t *testing.T) {
	c := harness.Up(t, harness.TopoFlat(4))
	nodes := map[string]*harness.Node{}
	for _, name := range []string{"hostA", "hostB", "hostC", "hostD"} {
		nodes[name] = c.Node(name)
		pkStartBroker(t, nodes[name], pkConfig())
	}
	ops := map[string]*harness.Session{}
	for _, name := range []string{"hostA", "hostB", "hostC", "hostD"} {
		ops[name] = pkOperator(t, nodes[name])
	}

	a, b, cNode, d := nodes["hostA"], nodes["hostB"], nodes["hostC"], nodes["hostD"]
	opA, opB, opC, opD := ops["hostA"], ops["hostB"], ops["hostC"], ops["hostD"]
	addrA := pkAddr(c, "hostA")
	usedStatic := []string{pkStatic16, pkStaticB, pkStaticReplace, pkStatic128, pkStaticRuntimeBad}

	// The fingerprint of A's identity is derived independently from its
	// certificate so that a connect result can be checked against it rather
	// than against itself.
	aFingerprint := pkIdentityFingerprint(t, a)

	t.Run("REQ-broker-pairing-006 key within validity", func(t *testing.T) {
		key := pkCreateKey(t, opA)
		expires, err := time.Parse(time.RFC3339, key.ExpiresAt)
		if err != nil {
			t.Fatalf("expires_at %q is not RFC 3339: %v", key.ExpiresAt, err)
		}
		if left := time.Until(expires); left <= 9*time.Minute || left > 10*time.Minute {
			t.Fatalf("the key expires in %s, want between 9 and 10 minutes", left)
		}

		address := pkShownAddress(t, c, "hostA", key.Addresses)
		r := pkConnect(t, opB, address, key.Key, "")
		r.MustOK(t)
		out := pkDecodeConnect(t, r)
		if out.Alias != "hostA-17443" {
			t.Fatalf("alias = %q, want hostA-17443", out.Alias)
		}
		if out.Fingerprint != aFingerprint {
			t.Fatalf("fingerprint = %q, want A's identity %q", out.Fingerprint, aFingerprint)
		}
		pkWaitConnected(t, b, "hostA-17443", pkRelinkBound)
		pkWaitConnected(t, a, "hostB-17443", pkRelinkBound)

		// This subtest leaves B paired; the reuse test below re-pairs it and
		// still expects A's only peer to be B.
		// The validity limit itself (the tenth minute and beyond) is covered by
		// internal/pairing/pairkey_expired_test.go's
		// TestREQBrokerPairing006_NineMinutesSucceedsElevenMinutesExpired and
		// internal/pairing/pairkey_test.go's
		// TestREQBrokerPairing006_KeyExpiredAtElevenMinutes.
	})

	t.Run("REQ-broker-pairing-007 one-time key used once", func(t *testing.T) {
		key := pkCreateKey(t, opA)
		address := pkShownAddress(t, c, "hostA", key.Addresses)

		pkConnect(t, opB, address, key.Key, "").MustOK(t)
		pkWaitConnected(t, b, "hostA-17443", pkRelinkBound)

		// A one-time key used once is gone, not expired; the acceptor's audit
		// reason tells the two causes apart.
		since := time.Now().Add(-time.Second)
		r := pkConnect(t, opC, address, key.Key, "")
		pkRejectReason(t, r, a, since, "hostC-17443", "key invalid")
		if !r.HasPrefix("Not connected") {
			t.Fatalf("the reuse error is %q, want it to start with Not connected", r.Text)
		}

		peers := pkPeers(t, a)
		if len(peers) != 1 || peers[0].GetAlias() != "hostB-17443" {
			t.Fatalf("A's peers are %v, want only hostB-17443", pkAliases(peers))
		}
	})

	t.Run("REQ-broker-pairing-008 no static key", func(t *testing.T) {
		// A runs the default configuration and therefore has no static key.
		for _, key := range []string{"hello-world-key-123", "wrong-key-one", "wrong-key-two", "wrong-key-three"} {
			since := time.Now().Add(-time.Second)
			r := pkConnect(t, opD, addrA, key, "")
			pkRejectReason(t, r, a, since, "hostD-17443", "key invalid")
			if !r.HasPrefix("Not connected") {
				t.Fatalf("a wrong key returned %q, want it to start with Not connected", r.Text)
			}
		}

		if peers := pkPeers(t, d); len(peers) != 0 {
			t.Fatalf("D has peers %v, want none", pkAliases(peers))
		}
		for _, peer := range pkPeers(t, a) {
			if peer.GetAlias() == "hostD-17443" {
				t.Fatalf("A listed D after four rejected attempts")
			}
		}
	})

	t.Run("REQ-broker-pairing-011 wrong key stores nothing", func(t *testing.T) {
		since := time.Now().Add(-time.Second)
		r := pkConnect(t, opD, addrA, "definitely-not-the-key", "")
		pkRejectReason(t, r, a, since, "hostD-17443", "key invalid")

		aData := pkPeersJSON(t, a)
		dData := pkPeersJSON(t, d)
		dFingerprint := pkIdentityFingerprint(t, d)
		if bytes.Contains(aData, []byte(dFingerprint)) {
			t.Fatalf("A's peers.json contains D's fingerprint %s", dFingerprint)
		}
		if bytes.Contains(dData, []byte(aFingerprint)) {
			t.Fatalf("D's peers.json contains A's fingerprint %s", aFingerprint)
		}
		for _, peer := range pkPeers(t, a) {
			if peer.GetAlias() == "hostD-17443" {
				t.Fatalf("A listed D after a failed pairing")
			}
		}
	})

	t.Run("REQ-broker-pairing-010 static key and plain address", func(t *testing.T) {
		pkRestart(t, a, pkConfig(pkStaticKeyYAML(pkStatic16)), opA)
		opA = pkOperator(t, a)

		// The address is written plainly as <A address>:17443 rather than
		// copied from the key's shown_addresses.
		r := pkConnect(t, opB, addrA, pkStatic16, "")
		r.MustOK(t)
		out := pkDecodeConnect(t, r)
		if out.Alias != "hostA-17443" {
			t.Fatalf("alias = %q, want hostA-17443", out.Alias)
		}
		if out.Fingerprint != aFingerprint {
			t.Fatalf("fingerprint = %q, want A's identity %q", out.Fingerprint, aFingerprint)
		}
		pkWaitConnected(t, b, "hostA-17443", pkRelinkBound)
	})

	t.Run("REQ-broker-pairing-009 static key many times", func(t *testing.T) {
		for name, op := range map[string]*harness.Session{
			"hostB": opB, "hostC": opC, "hostD": opD,
		} {
			r := pkConnect(t, op, addrA, pkStatic16, "")
			r.MustOK(t)
			if out := pkDecodeConnect(t, r); out.Fingerprint != aFingerprint {
				t.Fatalf("%s saw fingerprint %q, want A's identity %q", name, out.Fingerprint, aFingerprint)
			}
		}
		// "Still usable after 11 minutes" is covered by
		// internal/pairing/pairkey_test.go's
		// TestREQBrokerPairing009_StaticKeyNotExpired; waiting here would only
		// repeat the unit test's clock.
	})

	t.Run("REQ-broker-pairing-025 never generates a static key", func(t *testing.T) {
		pkRestart(t, a, pkConfig(), opA)
		opA = pkOperator(t, a)

		var keys []string
		for i := 0; i < 3; i++ {
			key := pkCreateKey(t, opA)
			if key.ExpiresAt == "" {
				t.Fatal("a generated key has no expires_at")
			}
			keys = append(keys, key.Key, strings.ReplaceAll(key.Key, "-", ""))
		}
		for _, needle := range keys {
			if pkDataDirContains(t, a, needle) {
				t.Fatalf("a generated one-time key is stored under %s/.agent-peer", a.AccountHome())
			}
		}

		pkRestart(t, a, pkConfig(pkStaticKeyYAML(pkStaticB)), opA)
		opA = pkOperator(t, a)
		pkConnect(t, opD, addrA, pkStaticB, "").MustOK(t)
	})

	t.Run("REQ-broker-pairing-029 static key length", func(t *testing.T) {
		// Start a Broker with a 15-character key and no MCP sessions running:
		// the startup must fail with the invalid-configuration exit code.
		opA.End(t)
		a.StopBroker(t, syscall.SIGTERM)
		start := a.StartBroker(t, harness.BrokerSpec{ConfigYAML: pkConfig(pkStaticKeyYAML(pkStaticShort))})
		if start.Running {
			t.Fatal("the Broker started with a 15-character static key")
		}
		if start.ExitCode != 5 {
			t.Fatalf("exit code = %d, want 5; stderr: %s", start.ExitCode, start.StderrLast)
		}
		if !strings.Contains(start.StderrLast, "pairing.static_key") {
			t.Fatalf("stderr %q does not name pairing.static_key", start.StderrLast)
		}

		pkStartBroker(t, a, pkConfig(pkStaticKeyYAML(pkStatic16)))
		opA = pkOperator(t, a)
		pkConnect(t, opD, addrA, pkStatic16, "").MustOK(t)

		pkRestart(t, a, pkConfig(pkStaticKeyYAML(pkStatic128)), opA)
		opA = pkOperator(t, a)
		pkConnect(t, opD, addrA, pkStatic128, "").MustOK(t)

		// An invalid replacement written while the Broker runs must be ignored:
		// the log line is the observable event, and the old value keeps
		// working for the next handshake.
		a.WriteConfig(t, pkConfig(pkStaticKeyYAML(pkStaticRuntimeBad)))
		harness.Eventually(t, pkWaitBound, "the Broker keeps the current invalid static key", func() (bool, string) {
			log := a.BrokerLog(t)
			kept := bytes.Contains(log, []byte("keeping the current value"))
			named := bytes.Contains(log, []byte("pairing.static_key"))
			if kept && named {
				return true, ""
			}
			return false, fmt.Sprintf("broker log has keeping=%v key=%v", kept, named)
		})
		pkConnect(t, opC, addrA, pkStatic128, "").MustOK(t)
		pkConnect(t, opD, addrA, pkStaticRuntimeBad, "").MustCode(t, "pairing_rejected")
	})

	t.Run("REQ-broker-pairing-033 replace static key", func(t *testing.T) {
		// B paired with the old static key in REQ-broker-pairing-009 and must
		// stay connected across the replacement.
		since := time.Now().Add(-time.Second)
		a.WriteConfig(t, pkConfig(pkStaticKeyYAML(pkStaticReplace)))
		pkWaitAuditConfigChange(t, a, "pairing.static_key", since)

		rejectedAt := time.Now().Add(-time.Second)
		pkRejectReason(t, pkConnect(t, opC, addrA, pkStatic128, ""), a, rejectedAt, "hostC-17443", "key invalid")
		pkConnect(t, opD, addrA, pkStaticReplace, "").MustOK(t)
		pkWaitConnected(t, b, "hostA-17443", pkRelinkBound)
	})

	t.Run("REQ-broker-pairing-032 remove static key, partial", func(t *testing.T) {
		// C must not already be a peer, or "both sides store nothing" says
		// nothing about the rejected attempt. Unpair it first.
		pkDisconnect(t, opA, "hostC-17443")
		pkDisconnect(t, opC, "hostA-17443")

		since := time.Now().Add(-time.Second)
		a.WriteConfig(t, pkConfig())
		pkWaitAuditConfigChange(t, a, "pairing.static_key", since)

		pkConnect(t, opC, addrA, pkStaticReplace, "").MustCode(t, "pairing_rejected")

		cFingerprint := pkIdentityFingerprint(t, cNode)
		if bytes.Contains(pkPeersJSON(t, a), []byte(cFingerprint)) {
			t.Fatalf("A's peers.json still contains C's fingerprint %s", cFingerprint)
		}
		if bytes.Contains(pkPeersJSON(t, cNode), []byte(aFingerprint)) {
			t.Fatalf("C's peers.json still contains A's fingerprint %s", aFingerprint)
		}
		audit := a.ReadFile(t, filepath.Join(a.AccountHome(), ".agent-peer", "audit.log"))
		if bytes.Contains(audit, []byte(pkStaticReplace)) {
			t.Fatal("the audit log contains the removed static key's value")
		}
		pkWaitConnected(t, b, "hostA-17443", pkRelinkBound)
	})

	t.Run("REQ-broker-pairing-030 log has no secret, partial", func(t *testing.T) {
		logs := a.Exec(t, []string{"sh", "-c",
			"cat /ctl/broker-*.log " + shellQuote(filepath.Join(a.AccountHome(), ".agent-peer", "audit.log")) + " 2>/dev/null"}, nil)
		if logs.ExitCode != 0 {
			t.Fatalf("reading A's logs: exit %d: %s", logs.ExitCode, logs.Stderr)
		}
		for _, value := range usedStatic {
			if strings.Contains(logs.Stdout, value) {
				t.Fatalf("A's logs or audit file contain the static key value %q", value)
			}
		}
		// The other half, reading the configuration through get_config, belongs
		// to the configuration hot-reload and audit work item.
	})

	t.Run("REQ-broker-pairing-015 unpaired without key rejected", func(t *testing.T) {
		before := pkPeers(t, a)
		reason := pkProbeUnpairedHello(t, c, "hostB", addrA)
		if reason != "REJECT_REASON_NOT_A_PEER" {
			t.Fatalf("reject reason = %q, want REJECT_REASON_NOT_A_PEER", reason)
		}
		for _, peer := range pkPeers(t, a) {
			if peer.GetAlias() == "probe" {
				t.Fatal("A listed the unpaired probe as a Broker")
			}
		}
		if after := pkPeers(t, a); len(after) != len(before) {
			t.Fatalf("A's peer count changed from %d to %d after the probe", len(before), len(after))
		}
	})
}

// pkConfig merges the shortened timers with the given fragments.
func pkConfig(parts ...string) string {
	return harness.ConfigYAML(append([]string{harness.FastTimers}, parts...)...)
}

// pkStaticKeyYAML writes the preset static key as a configuration fragment.
func pkStaticKeyYAML(key string) string {
	return fmt.Sprintf("pairing:\n  static_key: %q\n", key)
}

// pkAddr is the address of node's Broker on the flat network.
func pkAddr(c *harness.Cluster, node string) string {
	return net.JoinHostPort(c.Addr(node, "net"), "17443")
}

// pkStartBroker starts a Broker and fails when it does not come up. A Broker
// that another process (an MCP server process) started instead exits with code
// 3 here; the helper then waits for that process to answer.
func pkStartBroker(t *testing.T, node *harness.Node, config string) {
	t.Helper()
	start := node.StartBroker(t, harness.BrokerSpec{ConfigYAML: config})
	if start.Running {
		return
	}
	if start.ExitCode != 3 {
		t.Fatalf("the Broker did not start: exit code %d: %s", start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
	harness.Eventually(t, adPeerBrokerReadyBound, "the Broker started by another process answers", func() (bool, string) {
		res := node.Exec(t, []string{"/usr/local/bin/agent-peer", "status"}, nil)
		return res.ExitCode == 0, fmt.Sprintf("agent-peer status exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	})
}

// pkRestart stops the Broker and starts it with config. Every MCP server
// process passed in stop is ended first, because such a process launches a
// Broker of its own when the socket disappears and would race this restart.
func pkRestart(t *testing.T, node *harness.Node, config string, stop ...*harness.Session) {
	t.Helper()
	for _, s := range stop {
		s.End(t)
	}
	node.StopBroker(t, syscall.SIGTERM)
	pkStartBroker(t, node, config)
}

// pkOperator starts the operator session: an MCP server process without
// CLAUDE_CODE_MESSAGING_SOCKET. It never registers as an agent but can call
// the tools that manage pairing.
func pkOperator(t *testing.T, node *harness.Node) *harness.Session {
	t.Helper()
	s := node.StartSession(t, harness.SessionSpec{Name: "operator", StartMCP: true, NoSocketEnv: true})
	harness.Eventually(t, pkWaitBound, "the operator session reaches its Broker", func() (bool, string) {
		r := s.Call(t, "list_brokers", map[string]any{})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
	return s
}

// pkCreateKey creates one pairing key and returns its decoded result.
func pkCreateKey(t *testing.T, op *harness.Session) mcpserver.CreatePairingKeyOut {
	t.Helper()
	r := op.Call(t, "create_pairing_key", map[string]any{})
	r.MustOK(t)
	out, err := harness.Decode[mcpserver.CreatePairingKeyOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding create_pairing_key: %v", err)
	}
	return out
}

// pkConnect calls connect_broker and returns the raw result so the caller can
// assert an error code or decode a success.
func pkConnect(t *testing.T, op *harness.Session, address, key, localAlias string) harness.Result {
	t.Helper()
	args := map[string]any{"address": address, "key": key}
	if localAlias != "" {
		args["local_alias"] = localAlias
	}
	return op.Call(t, "connect_broker", args)
}

// pkRejectReason asserts the connect was rejected and that the acceptor
// audited the failure with reason since since. The acceptor owns the reason,
// so its audit record is where an invalid key and an expired one are told
// apart; the dialer only learns that the connection closed.
func pkRejectReason(t *testing.T, r harness.Result, acceptor *harness.Node, since time.Time, alias, reason string) {
	t.Helper()
	r.MustCode(t, "pairing_rejected")
	// The dialer learns the cause from the acceptor's reject frame. Both sides
	// are asserted: the tool text the dialer sees and the acceptor's audit.
	wantText := "invalid"
	if strings.Contains(reason, "expired") {
		wantText = "expired"
	}
	if !strings.Contains(r.Text, wantText) {
		t.Fatalf("the dialer's error is %q, want it to name %q", r.Text, wantText)
	}
	harness.Eventually(t, pkWaitBound, "the pairing failure is audited as "+reason, func() (bool, string) {
		events, detail := acceptor.Oracle().Audit(t, since, 0)
		if detail != nil {
			return false, detail.GetMessage()
		}
		var seen []string
		for _, event := range events {
			if event.GetEvent() != localpb.AuditEventKind_AUDIT_EVENT_KIND_PAIRING_FAILED {
				continue
			}
			if event.GetPeer().GetAlias() != alias {
				continue
			}
			seen = append(seen, event.GetDetail())
			if event.GetDetail() == reason {
				return true, ""
			}
		}
		return false, fmt.Sprintf("pairing_failed events for %s carry details %v", alias, seen)
	})
}

// pkDecodeConnect decodes a successful connect_broker result.
func pkDecodeConnect(t *testing.T, r harness.Result) mcpserver.ConnectBrokerOut {
	t.Helper()
	out, err := harness.Decode[mcpserver.ConnectBrokerOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding connect_broker: %v", err)
	}
	return out
}

// pkShownAddress picks the shown address of node on the flat network.
func pkShownAddress(t *testing.T, c *harness.Cluster, node string, shown []string) string {
	t.Helper()
	want := net.JoinHostPort(c.Addr(node, "net"), "17443")
	for _, address := range shown {
		if address == want {
			return address
		}
	}
	if len(shown) > 0 {
		return shown[0]
	}
	t.Fatalf("create_pairing_key returned no shown address")
	return ""
}

// pkPeers lists node's peers through the local interface probe.
func pkPeers(t *testing.T, node *harness.Node) []*localpb.BrokerInfo {
	t.Helper()
	resp, detail := node.Oracle().ListBrokers(t)
	if detail != nil {
		t.Fatalf("list_brokers: %s", detail.GetMessage())
	}
	return resp.GetBrokers()
}

// pkAliases renders the peer aliases for failure messages.
func pkAliases(peers []*localpb.BrokerInfo) []string {
	out := make([]string, 0, len(peers))
	for _, peer := range peers {
		out = append(out, peer.GetAlias())
	}
	return out
}

// pkWaitConnected waits until node lists the peer alias as connected.
func pkWaitConnected(t *testing.T, node *harness.Node, alias string, bound time.Duration) {
	t.Helper()
	harness.Eventually(t, bound, "the link to "+alias+" is connected", func() (bool, string) {
		resp, detail := node.Oracle().ListBrokers(t)
		if detail != nil {
			return false, detail.GetMessage()
		}
		for _, peer := range resp.GetBrokers() {
			if peer.GetAlias() != alias {
				continue
			}
			if peer.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
				return false, fmt.Sprintf("status is %s", peer.GetStatus())
			}
			return true, ""
		}
		return false, "the peer is not listed"
	})
}

// pkDisconnect unpairs broker using the operator tool.
func pkDisconnect(t *testing.T, op *harness.Session, broker string) {
	t.Helper()
	r := op.Call(t, "disconnect_broker", map[string]any{"broker": broker})
	r.MustOK(t)
}

// pkPeersJSON returns the peer table's bytes, or nil when it does not exist.
func pkPeersJSON(t *testing.T, node *harness.Node) []byte {
	t.Helper()
	path := filepath.Join(node.AccountHome(), ".agent-peer", "peers.json")
	res := node.Exec(t, []string{"sh", "-c", "if [ -f " + shellQuote(path) + " ]; then cat " + shellQuote(path) + "; fi"}, nil)
	if res.ExitCode != 0 {
		t.Fatalf("reading %s: exit %d: %s", path, res.ExitCode, res.Stderr)
	}
	return []byte(res.Stdout)
}

// pkDataDirContains reports whether any file under the account's .agent-peer
// directory contains needle.
func pkDataDirContains(t *testing.T, node *harness.Node, needle string) bool {
	t.Helper()
	dir := filepath.Join(node.AccountHome(), ".agent-peer")
	// The peers table is rewritten through a temporary file that is renamed
	// into place. A recursive grep can open one of those temporary files and
	// find it gone a moment later, so the search walks the durable files and
	// ignores a file that disappears between listing and reading.
	const script = `dir=$1; needle=$2; status=1
for f in $(find "$dir" -type f ! -name '*.tmp-*' 2>/dev/null); do
	if grep -qF -e "$needle" -- "$f" 2>/dev/null; then status=0; break; fi
done
exit $status`
	res := node.Exec(t, []string{"sh", "-c", script, "sh", dir, needle}, nil)
	switch res.ExitCode {
	case 0:
		return true
	case 1:
		return false
	default:
		t.Fatalf("searching %s for %q: exit %d: %s", dir, needle, res.ExitCode, res.Stderr)
		return false
	}
}

// pkIdentityFingerprint derives a node's fingerprint from its certificate:
// the SHA-256 of the DER SubjectPublicKeyInfo.
func pkIdentityFingerprint(t *testing.T, node *harness.Node) string {
	t.Helper()
	path := filepath.Join(node.AccountHome(), ".agent-peer", "identity.crt")
	data := node.ReadFile(t, path)
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s is not a PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// pkWaitAuditConfigChange waits for a config_file_changed record for key after
// since and returns it.
func pkWaitAuditConfigChange(t *testing.T, node *harness.Node, key string, since time.Time) *localpb.AuditEvent {
	t.Helper()
	var found *localpb.AuditEvent
	harness.Eventually(t, pkWaitBound, "the configuration file change of "+key+" is audited", func() (bool, string) {
		events, detail := node.Oracle().Audit(t, since, 0)
		if detail != nil {
			return false, detail.GetMessage()
		}
		for _, event := range events {
			if event.GetEvent() == localpb.AuditEventKind_AUDIT_EVENT_KIND_CONFIG_FILE_CHANGED && event.GetKey() == key {
				found = event
				return true, ""
			}
		}
		return false, "no config_file_changed record yet"
	})
	return found
}

// pkProbeUnpairedHello runs an unpaired Link Hello from node to addr and
// returns the reject reason. The probe runs inside the container, where addr
// is reachable; the node's own e2e-node binary performs it with node.ProbeHello.
func pkProbeUnpairedHello(t *testing.T, c *harness.Cluster, fromNode, addr string) string {
	t.Helper()
	res := c.Node(fromNode).Exec(t, []string{"/usr/local/bin/e2e-node", "probe-hello", "--addr", addr}, nil)
	if res.ExitCode != 0 {
		t.Fatalf("the link probe exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if reason, ok := strings.CutPrefix(strings.TrimSpace(line), "REASON="); ok {
			return reason
		}
	}
	t.Fatalf("the link probe printed no reason: %q", res.Stdout)
	return ""
}
