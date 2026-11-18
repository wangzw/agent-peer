//go:build darwin

package fsnotify

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestKqueueCloseDoesNotCloseAReusedDescriptor reproduces the kqueue
// double-close defect without racing goroutines.
//
// kqueue.remove closes a watch descriptor at backend_kqueue.go:320 and deletes
// it from the watches maps at :322. A concurrent Watcher.Close snapshots the
// maps inside that window, so it closes the same descriptor a second time. If
// the kernel has already handed that number to an unrelated open file, the
// second close hits that file.
//
// The test reconstructs the window directly: it registers and closes the watch
// descriptor exactly as remove does after a successful register, leaves the
// watches entry in place, and opens a sentinel file until the freed descriptor
// number is reused. Close must then leave the sentinel open; until the defect
// is fixed, Close closes the reused descriptor and the final Fstat reports
// EBADF.
func TestKqueueCloseDoesNotCloseAReusedDescriptor(t *testing.T) {
	ev := make(chan Event, 1)
	errs := make(chan error, 1)
	b, err := newBackend(ev, errs)
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	kq := b.(*kqueue)
	t.Cleanup(func() { _ = kq.Close() })

	watchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(watchDir, "watched"), []byte("watched"), 0o600); err != nil {
		t.Fatalf("writing watched file: %v", err)
	}
	if err := kq.Add(watchDir); err != nil {
		t.Fatalf("watching %s: %v", watchDir, err)
	}

	info, ok := kq.watches.byPath(watchDir)
	if !ok {
		t.Fatalf("no watch registered for %s", watchDir)
	}
	wd := info.wd

	// This is the state remove leaves behind after its successful close at
	// backend_kqueue.go:320: the descriptor is closed, but the watches maps
	// still route the path to it until remove deletes them at :322.
	if err := kq.register([]int{wd}, unix.EV_DELETE, 0); err != nil {
		t.Fatalf("registering EV_DELETE for descriptor %d: %v", wd, err)
	}
	if err := unix.Close(wd); err != nil {
		t.Fatalf("closing watch descriptor %d: %v", wd, err)
	}

	sentinelPath := filepath.Join(t.TempDir(), "sentinel")
	var sentinel *os.File
	for attempt := 0; attempt < 8; attempt++ {
		f, err := os.OpenFile(sentinelPath, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatalf("opening sentinel (attempt %d): %v", attempt+1, err)
		}
		if int(f.Fd()) == wd {
			sentinel = f
			break
		}
		_ = f.Close()
	}
	if sentinel == nil {
		t.Skipf("kernel did not reuse descriptor %d for the sentinel within 8 opens; this test needs descriptor reuse", wd)
	}
	t.Cleanup(func() {
		// The double close leaves the descriptor closed already; the error
		// from that is expected and not what this cleanup is testing.
		_ = sentinel.Close()
	})

	var before syscall.Stat_t
	if err := syscall.Fstat(int(sentinel.Fd()), &before); err != nil {
		t.Fatalf("sentinel fd %d: fstat before Close: %v", sentinel.Fd(), err)
	}

	if err := kq.Close(); err != nil {
		t.Fatalf("closing watcher: %v", err)
	}

	var after syscall.Stat_t
	if err := syscall.Fstat(int(sentinel.Fd()), &after); err != nil {
		t.Fatalf("sentinel fd %d: fstat after Close: %v; EBADF means Close closed the reused descriptor a second time (double close)", sentinel.Fd(), err)
	}
	if after.Ino != before.Ino {
		t.Fatalf("sentinel fd %d: inode changed after Close: expected %d, got %d", sentinel.Fd(), before.Ino, after.Ino)
	}
}

// TestAddWatchKeepsAnExistingDescriptorWhenReregisterFails covers the other
// path that can close a descriptor the watches maps still reference. When a
// path is already watched, addWatch registers the existing descriptor again;
// if that register fails, the descriptor belongs to the surviving watch and
// must stay open. Closing it would leave the mapping pointing at a number
// another open file may already own.
func TestAddWatchKeepsAnExistingDescriptorWhenReregisterFails(t *testing.T) {
	ev := make(chan Event, 1)
	errs := make(chan error, 1)
	b, err := newBackend(ev, errs)
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	kq := b.(*kqueue)

	watchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(watchDir, "watched"), []byte("watched"), 0o600); err != nil {
		t.Fatalf("writing watched file: %v", err)
	}
	if err := kq.Add(watchDir); err != nil {
		t.Fatalf("watching %s: %v", watchDir, err)
	}
	info, ok := kq.watches.byPath(watchDir)
	if !ok {
		t.Fatalf("no watch registered for %s", watchDir)
	}
	wd := info.wd

	// Closing the kqueue itself makes the next kevent registration fail, which
	// is the failure path of a re-registration on an already-watched path.
	if err := unix.Close(kq.kq); err != nil {
		t.Fatalf("closing the kqueue descriptor: %v", err)
	}
	if _, err := kq.addWatch(watchDir, 0, false); err == nil {
		t.Fatalf("re-registering %s after closing the kqueue succeeded, want an error", watchDir)
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(wd, &st); err != nil {
		t.Fatalf("watch descriptor %d: fstat after the failed re-registration: %v; the surviving watch lost its descriptor", wd, err)
	}
	info, ok = kq.watches.byPath(watchDir)
	if !ok || info.wd != wd {
		t.Fatalf("watch entry for %s = %+v (found %v), want wd %d", watchDir, info, ok, wd)
	}

	// ReadEvents is still waiting on the closed kqueue; closing the watcher
	// stops it. The patched Close leaves the surviving watch descriptor alone.
	if err := kq.Close(); err != nil {
		t.Fatalf("closing watcher: %v", err)
	}
}
