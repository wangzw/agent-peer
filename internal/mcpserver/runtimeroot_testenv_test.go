//go:build peers_testenv

package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRuntimeDirFromTestEnv checks that the test build hook points every local
// socket path at the same root the config test hook uses: root/<euid> for an
// ordinary account and root/root for the superuser.
func TestRuntimeDirFromTestEnv(t *testing.T) {
	root := os.Getenv("PEERS_TEST_RUNTIME_ROOT")
	if root == "" {
		t.Skip("PEERS_TEST_RUNTIME_ROOT is not set")
	}
	if got, want := userSocketPath(1000), filepath.Join(root, "1000", "broker.sock"); got != want {
		t.Errorf("userSocketPath(1000) = %q, want %q", got, want)
	}
	if got, want := systemSocketPath(), filepath.Join(root, "root", "broker.sock"); got != want {
		t.Errorf("systemSocketPath() = %q, want %q", got, want)
	}
}
