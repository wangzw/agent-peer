package inbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWatch_ConfigDirectoryAbsentAtStart covers a configuration directory that
// does not exist when the watcher starts. The watcher must follow the nearest
// existing ancestor instead of failing, call back once with 0 so the caller
// re-reads what it cares about, and move the watch down when the configuration
// directory and then its registry directory appear. The record written at the
// end is delivered only by a watcher that recovered downward.
func TestWatch_ConfigDirectoryAbsentAtStart(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("Watch on a configuration directory that does not exist yet: %v", err)
	}
	defer stop()

	// No watched directory identifies a record, so the startup callback is 0.
	aWaitPID(t, pids, 0)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating configuration directory: %v", err)
	}

	// The registry directory appears afterwards, inside the new configuration
	// directory, and the record written there must reach the callback.
	const pid = 7601
	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "agent",
		"messagingSocketPath": "/tmp/watch-startup-fallback.sock",
	})
	aWaitPID(t, pids, pid)
}

// TestWatch_SessionsDirectoryAppearsLater is the companion case: the
// configuration directory exists at startup and the registry directory appears
// later. The watch starts on the configuration directory and must move down
// when the registry directory and a record are created together.
func TestWatch_SessionsDirectoryAppearsLater(t *testing.T) {
	dir := t.TempDir()

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("Watch on a configuration directory without a registry directory: %v", err)
	}
	defer stop()

	const pid = 7602
	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "agent",
		"messagingSocketPath": "/tmp/watch-sessions-later.sock",
	})
	aWaitPID(t, pids, pid)
}
