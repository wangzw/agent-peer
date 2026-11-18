//go:build agentpeer_testenv

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTestRuntimeRoot(t *testing.T) {
	if got, want := testRuntimeRoot("/r", 1000), "/r/1000"; got != want {
		t.Errorf("testRuntimeRoot(%q, 1000) = %q, want %q", "/r", got, want)
	}
	if got, want := testRuntimeRoot("/r", 0), "/r/root"; got != want {
		t.Errorf("testRuntimeRoot(%q, 0) = %q, want %q", "/r", got, want)
	}
}

func TestTestEnvHooksInstalled(t *testing.T) {
	root := os.Getenv("AGENT_PEER_TEST_RUNTIME_ROOT")
	home := os.Getenv("AGENT_PEER_TEST_HOME")
	if root == "" || home == "" {
		t.Skip("AGENT_PEER_TEST_RUNTIME_ROOT and AGENT_PEER_TEST_HOME are not set")
	}
	if got, want := runtimeDirPath(1000), filepath.Join(root, "1000"); got != want {
		t.Errorf("runtimeDirPath(1000) = %q, want %q", got, want)
	}
	got, err := homeDirSource()
	if err != nil {
		t.Fatalf("homeDirSource() error = %v", err)
	}
	if got != home {
		t.Errorf("homeDirSource() = %q, want %q", got, home)
	}
}
