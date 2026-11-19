package inbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestWatch_SharesOneWatcher proves that watchers are shared: the second and
// third Watch calls for one configuration directory must keep using the
// watcher the first call built. Comparing the watcher pointer detects a second
// watcher that the count of map entries alone would not report.
func TestWatch_SharesOneWatcher(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
		t.Fatalf("creating sessions directory: %v", err)
	}

	stopFirst, err := Watch(dir, func(int) {})
	if err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	defer stopFirst()
	shared := sharedWatcherForTest(dir)
	if shared == nil {
		t.Fatal("no watcher is registered after the first Watch")
	}

	stopSecond, err := Watch(dir, func(int) {})
	if err != nil {
		t.Fatalf("second Watch: %v", err)
	}
	defer stopSecond()
	if got := sharedWatcherForTest(dir); got != shared {
		t.Fatal("the second Watch call built another watcher instead of sharing the first")
	}

	stopThird, err := Watch(dir, func(int) {})
	if err != nil {
		t.Fatalf("third Watch: %v", err)
	}
	if got := sharedWatcherForTest(dir); got != shared {
		t.Fatal("the third Watch call built another watcher instead of sharing the first")
	}

	stopSecond()
	if got := sharedWatcherForTest(dir); got != shared {
		t.Fatal("removing one registration replaced the shared watcher")
	}
	stopThird()
	stopFirst()
	if got := sharedWatcherForTest(dir); got != nil {
		t.Fatal("the watcher is still registered after the last registration was removed")
	}
}

// TestScanSessions_NoticesSameSizeSameMtimeRewrite is the unit test behind the
// watcher's deduplication key. The rewrite has the same size and the same
// modification time, so only the content digest can tell the two snapshots
// apart. Removing the digest would make changedPIDs return nothing here.
func TestScanSessions_NoticesSameSizeSameMtimeRewrite(t *testing.T) {
	dir := t.TempDir()
	fixed := time.Unix(1_700_000_000, 0)
	path := aWriteRecord(t, dir, 6401, map[string]any{
		"name":                "agent",
		"status":              "idle",
		"messagingSocketPath": "",
	})
	aSetModTime(t, path, fixed)

	before := scanSessions(aSessionsDir(dir))

	// "idle" and "busy" encode to the same number of bytes, and the rewrite is
	// given the same modification time as the first write.
	aWriteRecord(t, dir, 6401, map[string]any{
		"name":                "agent",
		"status":              "busy",
		"messagingSocketPath": "",
	})
	aSetModTime(t, path, fixed)

	after := scanSessions(aSessionsDir(dir))

	changed := changedPIDs(before, after)
	if len(changed) != 1 || changed[0] != 6401 {
		t.Fatalf("changedPIDs = %v, want [6401]; size and modification time are unchanged, so only the content digest can report this rewrite", changed)
	}
}

// TestWatch_RegistryDirectoryAppearsThenRewrite watches a configuration
// directory whose registry directory does not exist yet. The directory is
// created first, the appearance is observed, and only then is a record written
// and rewritten. This pins that the watch installed when the directory
// appeared actually covers later changes inside it.
func TestWatch_RegistryDirectoryAppearsThenRewrite(t *testing.T) {
	dir := t.TempDir()

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer stop()

	if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
		t.Fatalf("creating sessions directory: %v", err)
	}
	// The registry directory itself cannot be identified by a process id.
	aWaitPID(t, pids, 0)

	aWriteRecord(t, dir, 6751, map[string]any{
		"name":                "agent",
		"messagingSocketPath": "/tmp/watch-appears.sock",
	})
	aWaitPID(t, pids, 6751)

	aWriteRecord(t, dir, 6751, map[string]any{
		"name":                "renamed",
		"messagingSocketPath": "/tmp/watch-appears.sock",
	})
	aWaitPID(t, pids, 6751)
}

// TestWatch_FileWatcherErrorCallsBackWithZero injects a file watcher error and
// requires a callback with 0, so a caller re-reads what it cares about instead
// of the error being dropped.
func TestWatch_FileWatcherErrorCallsBackWithZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
		t.Fatalf("creating sessions directory: %v", err)
	}

	pids := make(chan int, 16)
	w, err := newWatcher(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	t.Cleanup(w.shutdown)

	// Deliver the error through the watcher's own handler. Writing to
	// fsw.Errors instead would race with the fsnotify backend goroutine, which
	// reads that channel itself.
	w.handleWatchError()

	aWaitPID(t, pids, 0)
}

// TestWatch_RegistryDirectoryRemovedAndRecreated is the regression test for
// the lost watch. The registry directory is removed and recreated before its
// removal event is examined, so the watcher sees the path existing again even
// though the operating system has already dropped the watch. The record is
// rewritten afterwards; only a watcher that rebuilt its watch regardless of
// its recorded state receives that change.
func TestWatch_RegistryDirectoryRemovedAndRecreated(t *testing.T) {
	dir := t.TempDir()
	const pid = 6901
	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "before",
		"messagingSocketPath": "/tmp/watch-recreate.sock",
	})

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(p int) { pids <- p })
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer stop()

	// Recreate the directory while its removal event is being handled: the
	// Stat that follows must not conclude that nothing happened. Only recreate
	// when the directory is really absent. The first event after RemoveAll may
	// still be the record's removal while RemoveAll has not reached the
	// directory itself; recreating it then would let RemoveAll delete it again
	// and leave the outcome dependent on scheduling. Consuming the Once only
	// after an absence check makes the recreation land after RemoveAll has
	// finished, so the directory stays in place when the removal event is
	// examined.
	var recreated sync.Once
	beforeHandle := func() {
		if info, err := os.Stat(aSessionsDir(dir)); err == nil && info.IsDir() {
			return
		}
		recreated.Do(func() {
			if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
				t.Errorf("recreating sessions directory: %v", err)
			}
		})
	}
	testWatchBeforeHandleEvent.Store(&beforeHandle)
	t.Cleanup(func() { testWatchBeforeHandleEvent.Store(nil) })

	// The removal is fully handled once this closes, including the
	// reconciliation that leaves a watch covering the registry directory.
	//
	// The directory's own removal notification is not a reliable signal: the
	// watcher may reconcile before that notification is examined. When the
	// record's removal is the event being handled and RemoveAll removes the
	// directory before the reconciliation runs, the watcher moves its watch to
	// the configuration directory and removes the watch on the registry
	// directory. The queued removal notification then belongs to a watch the
	// watcher has already dropped, so it is never delivered. That is a
	// legitimate outcome: the directory is absent and the configuration watch
	// covers its recreation. Accept it by requiring only that the directory is
	// absent after an event has been handled; the reconciliation has then run
	// and moved the watch to an ancestor.
	removalHandled := make(chan struct{})
	signalHandled := func() {
		select {
		case <-removalHandled:
		default:
			close(removalHandled)
		}
	}
	afterHandle := func(event fsnotify.Event) {
		if filepath.Clean(event.Name) == aSessionsDir(dir) &&
			event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			signalHandled()
			return
		}
		if _, err := os.Stat(aSessionsDir(dir)); err != nil {
			signalHandled()
		}
	}
	testWatchAfterHandleEvent.Store(&afterHandle)
	t.Cleanup(func() { testWatchAfterHandleEvent.Store(nil) })

	if err := os.RemoveAll(aSessionsDir(dir)); err != nil {
		t.Fatalf("removing sessions directory: %v", err)
	}

	select {
	case <-removalHandled:
	case <-time.After(aWaitTimeout):
		t.Fatal("the watcher did not handle the registry directory's removal")
	}

	// Callbacks caused by the removal itself are not what this test is about.
	aDrain(pids)

	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "after",
		"messagingSocketPath": "/tmp/watch-recreate.sock",
	})
	aWaitPID(t, pids, pid)
}

// TestWatch_RegistryDirectoryCreatedBetweenStatAndAdd closes the second
// window: the registry directory is recreated after the watcher has checked
// that it is absent and before the watch on the configuration directory is
// installed. The creation notification is never delivered because the parent
// is not watched at that moment, so only a check after Add can discover it.
// The record is written after the removal event has been handled; a watcher
// that did not look again does not receive it.
func TestWatch_RegistryDirectoryCreatedBetweenStatAndAdd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
		t.Fatalf("creating sessions directory: %v", err)
	}

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer stop()

	// Recreate the directory in the window between the existence check and
	// Add. It stays empty; the record is written only after the event has been
	// handled below.
	var created sync.Once
	statAddWindow := func() {
		created.Do(func() {
			if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
				t.Errorf("creating sessions directory in the Stat/Add window: %v", err)
			}
		})
	}
	testWatchAfterStatBeforeAdd.Store(&statAddWindow)
	t.Cleanup(func() { testWatchAfterStatBeforeAdd.Store(nil) })

	removalHandled := make(chan struct{})
	afterHandle := func(event fsnotify.Event) {
		if filepath.Clean(event.Name) != aSessionsDir(dir) {
			return
		}
		if event.Op&(fsnotify.Remove|fsnotify.Rename) == 0 {
			return
		}
		select {
		case <-removalHandled:
		default:
			close(removalHandled)
		}
	}
	testWatchAfterHandleEvent.Store(&afterHandle)
	t.Cleanup(func() { testWatchAfterHandleEvent.Store(nil) })

	if err := os.RemoveAll(aSessionsDir(dir)); err != nil {
		t.Fatalf("removing sessions directory: %v", err)
	}

	select {
	case <-removalHandled:
	case <-time.After(aWaitTimeout):
		t.Fatal("the watcher did not handle the registry directory's removal")
	}
	aDrain(pids)

	const pid = 6851
	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "agent",
		"messagingSocketPath": "/tmp/watch-stat-add.sock",
	})
	aWaitPID(t, pids, pid)
}

// TestWatch_ConfigDirectoryRemovedAndRecreated covers the loss of the
// configuration directory itself. The watcher cannot install a watch on any
// directory it was following, so it must call back with 0 rather than fail
// silently, then pick the watch up again from the nearest existing ancestor
// when the directory reappears.
func TestWatch_ConfigDirectoryRemovedAndRecreated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
		t.Fatalf("creating sessions directory: %v", err)
	}

	pids := make(chan int, 16)
	stop, err := Watch(dir, func(pid int) { pids <- pid })
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer stop()

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing configuration directory: %v", err)
	}
	// No watched directory remains, so the caller is told to re-read
	// everything it cares about.
	aWaitPID(t, pids, 0)
	aDrain(pids)

	// Recreate the configuration directory; the ancestor watch reports it and
	// the watcher moves back down.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("recreating configuration directory: %v", err)
	}

	const pid = 7001
	aWriteRecord(t, dir, pid, map[string]any{
		"name":                "agent",
		"messagingSocketPath": "/tmp/watch-config-recreate.sock",
	})
	aWaitPID(t, pids, pid)
}
