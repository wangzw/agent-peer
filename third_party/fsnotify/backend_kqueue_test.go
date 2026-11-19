//go:build freebsd || openbsd || netbsd || dragonfly || darwin

package fsnotify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	kq := w.b.(*kqueue)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(wantUser, wantTotal int) {
		t.Helper()

		if len(kq.watches.path) != wantTotal {
			var d []string
			for k, v := range kq.watches.path {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.path (have %d, want %d):\n%v",
				len(kq.watches.path), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.wd) != wantTotal {
			var d []string
			for k, v := range kq.watches.wd {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.wd (have %d, want %d):\n%v",
				len(kq.watches.wd), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.byUser) != wantUser {
			var d []string
			for k, v := range kq.watches.byUser {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byUser (have %d, want %d):\n%v",
				len(kq.watches.byUser), wantUser, strings.Join(d, "\n"))
		}
	}

	check(2, 3)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(2, 3)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1, 2)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0, 0)

	// Don't check these after ever remove since they don't map easily to number
	// of files watches. Just make sure they're 0 after everything is removed.
	{
		want := 0
		if len(kq.watches.byDir) != want {
			var d []string
			for k, v := range kq.watches.byDir {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byDir (have %d, want %d):\n%v",
				len(kq.watches.byDir), want, strings.Join(d, "\n"))
		}

		if len(kq.watches.seen) != want {
			var d []string
			for k, v := range kq.watches.seen {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.seen (have %d, want %d):\n%v",
				len(kq.watches.seen), want, strings.Join(d, "\n"))
			return
		}
	}
}

// TestCloseClearsWatchState is a regression test for
// https://github.com/fsnotify/fsnotify/issues/732: Close() previously
// called w.Remove for every registered path after marking the watcher
// closed, but remove() short-circuits on isClosed() and never closes
// the underlying kqueue fds or drops the bookkeeping maps. Long-running
// processes that recreate watchers hit EMFILE because every watched
// directory and each file inside it leaked its fd on Close.
func TestCloseClearsWatchState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	kq := w.b.(*kqueue)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// After Close() every watch map should be drained. Pre-fix these
	// stayed populated because remove() returned nil without touching
	// the wd map.
	if got := len(kq.watches.path); got != 0 {
		t.Errorf("watches.path not drained after Close(): %d entries", got)
	}
	if got := len(kq.watches.wd); got != 0 {
		t.Errorf("watches.wd not drained after Close(): %d entries", got)
	}
	if got := len(kq.watches.byUser); got != 0 {
		t.Errorf("watches.byUser not drained after Close(): %d entries", got)
	}
	if got := len(kq.watches.byDir); got != 0 {
		t.Errorf("watches.byDir not drained after Close(): %d entries", got)
	}
}

// TestWriteAfterCreateIsReported verifies the guarantee the inbox relies on:
// once a Create event has been sent for a new file, a write to that file is
// reported as a Write event. The kqueue backend announces a new file found by
// scanning its directory, and a vnode watch registered only after that
// announcement misses a write made in between, because kqueue reports nothing
// that happened before the watch existed.
//
// The test holds the backend at that point, writes the file, and then requires
// the Write event.
func TestWriteAfterCreateIsReported(t *testing.T) {
	dir := t.TempDir()
	w := newWatcher(t, dir)
	defer w.Close()

	events := make(chan Event, 16)
	go func() {
		for event := range w.Events {
			events <- event
		}
	}()

	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() { close(reached) })
		<-release
	}
	testBeforeCreateEvent.Store(&hook)
	t.Cleanup(func() {
		testBeforeCreateEvent.Store(nil)
		select {
		case <-release:
		default:
			close(release)
		}
	})

	path := filepath.Join(dir, "record.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating %q: %s", path, err)
	}

	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("the backend did not reach the new file")
	}

	if err := os.WriteFile(path, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatalf("writing %q: %s", path, err)
	}
	close(release)

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if filepath.Clean(event.Name) != path {
				continue
			}
			if event.Op.Has(Write) {
				return
			}
		case <-timer.C:
			t.Fatalf("no Write event after writing %q; a write made after Create was lost", path)
		}
	}
}
