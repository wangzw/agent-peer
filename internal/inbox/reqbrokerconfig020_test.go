package inbox

import (
	"path/filepath"
	"testing"
)

// TestREQBrokerConfig020_LookupSocketPathForOwnerCheck is a supporting test: it
// supplies the two inputs the broker needs when it verifies that a registered
// inbox socket belongs to the calling account, namely the socket path exactly
// as the record carries it and the resolved configuration directory. The owner
// comparison itself, and refusing another account's session, are done by the
// broker and are covered in the broker unit.
func TestREQBrokerConfig020_LookupSocketPathForOwnerCheck(t *testing.T) {
	// REQ-broker-config-020
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 7101, map[string]any{
		"name":                "api-worker",
		"status":              "idle",
		"messagingSocketPath": socket,
	})

	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if s.SocketPath != socket {
		t.Fatalf("SocketPath = %q, want the record's value %q", s.SocketPath, socket)
	}
	if s.ConfigDir != dir {
		t.Fatalf("ConfigDir = %q, want the resolved %q", s.ConfigDir, dir)
	}
}
