package broker

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testWatchLogger() *slog.Logger {
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	return NewLogger(io.Discard, level)
}

// TestConfigDirWatchReportsChanges checks that a write to the configuration
// file wakes the diagnostics-only retry, and that a burst is debounced into one
// wake.
func TestConfigDirWatchReportsChanges(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configFile, []byte("broker: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	w := startConfigDirWatch(configFile, testWatchLogger())
	t.Cleanup(w.stopNow)

	if err := os.WriteFile(configFile, []byte("broker:\n  listen: ''\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	select {
	case <-w.events:
	case <-time.After(3 * time.Second):
		t.Fatal("the configuration write did not wake the retry")
	}

	// A burst inside the debounce window produces no second wake.
	if err := os.WriteFile(configFile, []byte("broker:\n  listen: '127.0.0.1:17443'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("broker:\n  listen: '127.0.0.1:17444'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	select {
	case <-w.events:
	case <-time.After(3 * time.Second):
		t.Fatal("the burst did not wake the retry")
	}
	select {
	case <-w.events:
		t.Fatal("the burst produced a second wake")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestConfigDirWatchStopClosesCleanly checks that stopping closes the watcher
// and that a write afterwards neither wakes the retry nor blocks shutdown.
func TestConfigDirWatchStopClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configFile, []byte("broker: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	w := startConfigDirWatch(configFile, testWatchLogger())
	w.stopNow()
	select {
	case <-w.done:
	default:
		t.Fatal("stopNow returned before the watcher finished")
	}
	if err := os.WriteFile(configFile, []byte("broker:\n  listen: ''\n"), 0o600); err != nil {
		t.Fatalf("WriteFile after stop: %v", err)
	}
	select {
	case <-w.events:
		t.Fatal("a write after stop woke the retry")
	case <-time.After(100 * time.Millisecond):
	}
	w.stopNow() // idempotent
}

// TestConfigDirWatchWithoutConfigFileIsInert covers a Load failure that left no
// configuration path: the watcher is inert and the minute tick still retries.
func TestConfigDirWatchWithoutConfigFileIsInert(t *testing.T) {
	w := startConfigDirWatch("", testWatchLogger())
	select {
	case <-w.done:
	default:
		t.Fatal("the inert watcher did not finish")
	}
	w.stopNow()
}
