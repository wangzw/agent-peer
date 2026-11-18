//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

const (
	// permConfigPath is the system Broker's configuration file.
	permConfigPath = "/etc/agent-peer/config.yaml"
	// permSocketPath is the system Broker's local socket.
	permSocketPath = "/run/agent-peer/broker.sock"
	// permPeerListen is the user-level Broker's listen address in the pairing
	// test.
	permPeerListen = "127.0.0.1:17444"
)

// TestREQBrokerConfig023_NoPermissionStillWorksOnSystemBroker checks that an
// account with no permission on the configuration file can still register,
// join and leave topics, list what it may see, and exchange messages on the
// system Broker.
func TestREQBrokerConfig023_NoPermissionStillWorksOnSystemBroker(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n")

	peer := syscontainer.NewSession(t, "peer", "peeragent")
	permWaitRegistered(t, peer)
	other := syscontainer.NewSession(t, "other", "otheragent")
	permWaitRegistered(t, other)

	for _, call := range []struct {
		session *syscontainer.Session
		tool    string
		args    map[string]any
	}{
		{other, "list_agents", map[string]any{}},
		{other, "list_topics", map[string]any{}},
		{other, "list_brokers", map[string]any{}},
		{peer, "join_topic", map[string]any{"name": "e2e/p"}},
		{other, "join_topic", map[string]any{"name": "e2e/p"}},
		{other, "leave_topic", map[string]any{"name": "e2e/p"}},
		{other, "join_topic", map[string]any{"name": "e2e/p"}},
	} {
		if text, isError := call.session.Call(t, call.tool, call.args); isError {
			t.Fatalf("%s as %s reported an error: %s", call.tool, call.session.Account, text)
		}
	}

	to := permSelfAddress(t, peer)
	const message = "hello from an account without config permission"
	if text, isError := other.Call(t, "send_message",
		map[string]any{"to": to, "message": message}); isError {
		t.Fatalf("send_message as other reported an error: %s", text)
	}
	frame, ok := peer.Fake.WaitFrame(3 * time.Second)
	if !ok {
		t.Fatalf("peer received no message from other")
	}
	if !strings.Contains(frame.Content, message) {
		t.Errorf("peer's frame content = %q, want it to contain %q", frame.Content, message)
	}
}

// TestREQBrokerConfig021_WriteNeedsConfigWritePermission checks that every RPC
// that changes configuration state is refused without write permission on the
// configuration file, and that a grant takes effect without restarting the
// caller's MCP server.
func TestREQBrokerConfig021_WriteNeedsConfigWritePermission(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n")
	syscontainer.StartUserBroker(t, "peer", "--listen", ":17444")

	peer := syscontainer.NewSession(t, "peer", "peeragent")
	permWaitRegistered(t, peer)
	other := syscontainer.NewSession(t, "other", "otheragent")
	permWaitConnected(t, other)

	peerAlias := permConfigValue(t, peer, "broker.alias")
	deniedKey := permPairingKey(t, peer)
	hashBefore := permFileSHA256(t, permConfigPath)

	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"set_config", map[string]any{"key": "broker.alias", "value": "c-alias"}},
		{"create_pairing_key", map[string]any{}},
		{"connect_broker", map[string]any{"address": permPeerListen, "key": deniedKey}},
		{"disconnect_broker", map[string]any{"broker": peerAlias}},
	} {
		text, isError := other.Call(t, call.tool, call.args)
		if !isError {
			t.Errorf("%s as other without write permission succeeded: %s", call.tool, text)
			continue
		}
		if !strings.Contains(text, "(code: permission_denied)") {
			t.Errorf("%s error = %q, want (code: permission_denied)", call.tool, text)
		}
	}

	if hashAfter := permFileSHA256(t, permConfigPath); hashAfter != hashBefore {
		t.Errorf("the refused calls changed the configuration file:\nbefore %s\nafter  %s", hashBefore, hashAfter)
	}

	root := syscontainer.NewSession(t, "root", "admin")
	permWaitConnected(t, root)
	text, isError := root.Call(t, "list_brokers", map[string]any{})
	if isError {
		t.Fatalf("list_brokers as root: %s", text)
	}
	var listed struct {
		Brokers []struct {
			Alias  string `json:"alias"`
			IsPeer bool   `json:"is_peer"`
		} `json:"brokers"`
	}
	if err := json.Unmarshal([]byte(text), &listed); err != nil {
		t.Fatalf("decoding list_brokers result %q: %v", text, err)
	}
	for _, broker := range listed.Brokers {
		if broker.IsPeer {
			t.Errorf("the system Broker lists peer %q after the refused connect", broker.Alias)
		}
	}

	// other's account is added to agentpeer-admins in the account database; the
	// running MCP server keeps its old group list, so only a fresh check of the
	// database can see the grant.
	permGrantAdmins(t, "0660")

	if text, isError := other.Call(t, "set_config",
		map[string]any{"key": "broker.alias", "value": "c-alias"}); isError {
		t.Fatalf("set_config as other after the grant: %s", text)
	}
	if text, isError := other.Call(t, "create_pairing_key", map[string]any{}); isError {
		t.Fatalf("create_pairing_key as other after the grant: %s", text)
	}

	key := permPairingKey(t, peer)
	text, isError = other.Call(t, "connect_broker",
		map[string]any{"address": permPeerListen, "key": key})
	if isError {
		t.Fatalf("connect_broker as other after the grant: %s", text)
	}
	var connected struct {
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal([]byte(text), &connected); err != nil {
		t.Fatalf("decoding connect_broker result %q: %v", text, err)
	}
	if connected.Alias == "" {
		t.Fatalf("connect_broker returned no peer alias: %s", text)
	}
	if text, isError := other.Call(t, "disconnect_broker",
		map[string]any{"broker": connected.Alias}); isError {
		t.Fatalf("disconnect_broker as other after the grant: %s", text)
	}
}

// TestREQBrokerConfig022_ReadNeedsConfigReadPermission checks that get_config
// is refused without read permission, that a group grant takes effect without
// a restart, and that an access control list entry is honoured even when the
// permission bits alone would deny it.
func TestREQBrokerConfig022_ReadNeedsConfigReadPermission(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n")

	other := syscontainer.NewSession(t, "other", "otheragent")
	permWaitConnected(t, other)

	text, isError := other.Call(t, "get_config", map[string]any{})
	if !isError {
		t.Fatalf("get_config as other without read permission succeeded: %s", text)
	}
	if strings.Contains(text, "broker.alias") || strings.Contains(text, "entries") {
		t.Errorf("the refused get_config result names configuration values: %s", text)
	}

	permGrantAdmins(t, "0640")

	text, isError = other.Call(t, "get_config", map[string]any{})
	if isError {
		t.Fatalf("get_config as other after the grant: %s", text)
	}
	if !strings.Contains(text, "broker.alias") {
		t.Errorf("get_config as other after the grant does not name broker.alias: %s", text)
	}

	// peer is not in agentpeer-admins and the mode stays 0640; only the ACL
	// grants it read access.
	permRunRoot(t, "setfacl", "-m", "u:peer:r", permConfigPath)
	if info, err := os.Stat(permConfigPath); err != nil {
		t.Fatalf("syscontainer: stat %s: %v", permConfigPath, err)
	} else if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("the configuration file mode = %04o after setfacl, want 0640", got)
	}

	peer := syscontainer.NewSession(t, "peer", "peeragent")
	permWaitConnected(t, peer)
	text, isError = peer.Call(t, "get_config", map[string]any{})
	if isError {
		t.Fatalf("get_config as peer after the ACL entry: %s", text)
	}
	if !strings.Contains(text, "broker.alias") {
		t.Errorf("get_config as peer after the ACL entry does not name broker.alias: %s", text)
	}
}

// TestREQBrokerConfig010_StatusWithoutReadPermissionShowsOwnAgents checks that
// status without read permission returns only the caller's own agents and no
// peers, instead of the full status or a refusal.
func TestREQBrokerConfig010_StatusWithoutReadPermissionShowsOwnAgents(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n")

	peer := syscontainer.NewSession(t, "peer", "peeragent")
	permWaitRegistered(t, peer)
	other := syscontainer.NewSession(t, "other", "otheragent")
	permWaitRegistered(t, other)
	for _, call := range []struct {
		session *syscontainer.Session
		topic   string
	}{
		{peer, "e2e/peer"},
		{other, "e2e/other"},
	} {
		if text, isError := call.session.Call(t, "join_topic",
			map[string]any{"name": call.topic}); isError {
			t.Fatalf("join_topic as %s: %s", call.session.Account, text)
		}
	}

	harness.Eventually(t, 3*time.Second, "the root status to list both agents", func() (bool, string) {
		res := syscontainer.RunAs(t, "root", nil, "status")
		ok := res.ExitCode == 0 &&
			strings.Contains(res.Stdout, "peeragent@sys") &&
			strings.Contains(res.Stdout, "otheragent@sys")
		return ok, res.Stdout + res.Stderr
	})

	res := syscontainer.RunAs(t, "other", nil, "status")
	if res.ExitCode != 0 {
		t.Fatalf("status as other: exit %d\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "otheragent@sys") || !strings.Contains(res.Stdout, "e2e/other") {
		t.Errorf("status as other does not show its own agent and topic:\n%s", res.Stdout)
	}

	// The filtered result is stable: other's status must keep excluding peer's
	// agent, peer's topic and every peer Broker. Both agents are registered
	// already, so a one-second observation is enough.
	harness.Consistently(t, time.Second, "other's status never shows peer's agent or a peer", func() (bool, string) {
		res := syscontainer.RunAs(t, "other", nil, "status")
		if res.ExitCode != 0 {
			return false, res.Stderr
		}
		if strings.Contains(res.Stdout, "peeragent") || strings.Contains(res.Stdout, "e2e/peer") {
			return false, res.Stdout
		}
		if !strings.HasSuffix(res.Stdout, "Peers: none\n") {
			return false, res.Stdout
		}
		return true, res.Stdout
	})
}

// TestREQBrokerConfig009_AuditNeedsReadPermissionOnSystemBroker checks the
// system half of REQ-broker-config-009: the audit log needs read permission on
// the system Broker's configuration file, and a grant takes effect without a
// restart.
func TestREQBrokerConfig009_AuditNeedsReadPermissionOnSystemBroker(t *testing.T) {
	if !syscontainer.InContainer() {
		syscontainer.Run(t, syscontainer.Plain)
		return
	}

	syscontainer.StartSystemBroker(t, "broker:\n  alias: sys\n")

	root := syscontainer.NewSession(t, "root", "admin")
	permWaitConnected(t, root)
	if text, isError := root.Call(t, "set_config",
		map[string]any{"key": "broker.alias", "value": "sys2"}); isError {
		t.Fatalf("set_config as root: %s", text)
	}

	denied := syscontainer.RunAs(t, "other", nil, "audit")
	if denied.ExitCode == 0 {
		t.Errorf("audit as other without read permission succeeded:\n%s", denied.Stdout)
	}
	if !strings.Contains(denied.Stderr, "permission_denied") {
		t.Errorf("audit as other without read permission does not explain the denial:\n%s", denied.Stderr)
	}

	permGrantAdmins(t, "0640")

	harness.Eventually(t, 3*time.Second, "other's audit to show the configuration change", func() (bool, string) {
		res := syscontainer.RunAs(t, "other", nil, "audit")
		if res.ExitCode != 0 {
			return false, res.Stderr
		}
		ok := strings.Contains(res.Stdout, "config_set") && strings.Contains(res.Stdout, "key=broker.alias")
		return ok, res.Stdout
	})
}

// permWaitRegistered waits until the session's MCP server has registered it.
func permWaitRegistered(t *testing.T, session *syscontainer.Session) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the session "+session.Account+" to register", func() (bool, string) {
		text, isError := session.Call(t, "list_agents", map[string]any{})
		return !isError, text
	})
}

// permWaitConnected waits until the session's MCP server has reached a Broker.
func permWaitConnected(t *testing.T, session *syscontainer.Session) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the MCP server of "+session.Account+" to reach its Broker",
		func() (bool, string) {
			text, isError := session.Call(t, "list_brokers", map[string]any{})
			return !isError, text
		})
}

// permSelfAddress returns the session's own agent address.
func permSelfAddress(t *testing.T, session *syscontainer.Session) string {
	t.Helper()
	text, isError := session.Call(t, "list_agents", map[string]any{})
	if isError {
		t.Fatalf("list_agents as %s: %s", session.Account, text)
	}
	var out struct {
		Self string `json:"self_address"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding list_agents result %q: %v", text, err)
	}
	if out.Self == "" {
		t.Fatalf("list_agents as %s returned no self_address: %s", session.Account, text)
	}
	return out.Self
}

// permConfigValue returns one configuration value read from the session's
// Broker.
func permConfigValue(t *testing.T, session *syscontainer.Session, key string) string {
	t.Helper()
	text, isError := session.Call(t, "get_config", map[string]any{"key": key})
	if isError {
		t.Fatalf("get_config %s as %s: %s", key, session.Account, text)
	}
	var out struct {
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding get_config result %q: %v", text, err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Key != key {
		t.Fatalf("get_config %s as %s returned %s", key, session.Account, text)
	}
	return out.Entries[0].Value
}

// permPairingKey creates a one-time pairing key at the session's Broker.
func permPairingKey(t *testing.T, session *syscontainer.Session) string {
	t.Helper()
	text, isError := session.Call(t, "create_pairing_key", map[string]any{})
	if isError {
		t.Fatalf("create_pairing_key as %s: %s", session.Account, text)
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decoding create_pairing_key result %q: %v", text, err)
	}
	if out.Key == "" {
		t.Fatalf("create_pairing_key as %s returned no key: %s", session.Account, text)
	}
	return out.Key
}

// permFileSHA256 returns the hexadecimal SHA-256 of path's bytes.
func permFileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("syscontainer: reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// permGrantAdmins adds other to agentpeer-admins and gives that group access to
// the configuration file with mode. It changes neither other's running MCP
// server nor the Broker.
func permGrantAdmins(t *testing.T, mode string) {
	t.Helper()
	permRunRoot(t, "chgrp", "agentpeer-admins", permConfigPath)
	permRunRoot(t, "chmod", mode, permConfigPath)
	permRunRoot(t, "usermod", "-aG", "agentpeer-admins", "other")
}

// permRunRoot runs a command as root and fails the test when it does not
// succeed.
func permRunRoot(t *testing.T, argv ...string) {
	t.Helper()
	cmd := syscontainer.Command("root", nil, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("syscontainer: %s: %v\n%s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
}
