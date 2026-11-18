package broker

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localSocketTestPath returns a path short enough for a Unix socket. t.TempDir
// can exceed the platform's sun_path limit, so the fallback lives directly in
// the temporary directory.
func localSocketTestPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.sock")
	if len(path) < 100 {
		return path
	}
	dir, err := os.MkdirTemp("", "ap")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "broker.sock")
}

func TestBindLocalSocketReplacesStaleSocket(t *testing.T) {
	path := localSocketTestPath(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix stale: %v", err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("Close stale listener: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket was not left in place: %v", err)
	}

	sock, err := BindLocalSocket(path, 0o600)
	if err != nil {
		t.Fatalf("BindLocalSocket: %v", err)
	}
	defer func() { _ = sock.Close(true) }()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial bound socket: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close dialed connection: %v", err)
	}
}

func TestBindLocalSocketRefusesRegularFile(t *testing.T) {
	path := localSocketTestPath(t)
	content := []byte("do not replace")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := BindLocalSocket(path, 0o600); err == nil {
		t.Fatal("BindLocalSocket succeeded over a regular file")
	} else if !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("BindLocalSocket error = %q, want it to say not a socket", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("file content changed to %q, want %q", got, content)
	}
	if info, err := os.Lstat(path); err != nil {
		t.Fatalf("Lstat: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("path is no longer a regular file: %s", info.Mode())
	}
}

func TestBindLocalSocketPermission(t *testing.T) {
	path := localSocketTestPath(t)
	sock, err := BindLocalSocket(path, 0o600)
	if err != nil {
		t.Fatalf("BindLocalSocket: %v", err)
	}
	defer func() { _ = sock.Close(true) }()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}
}

func TestLocalSocketCloseDoesNotRemoveReplacement(t *testing.T) {
	path := localSocketTestPath(t)
	a, err := BindLocalSocket(path, 0o600)
	if err != nil {
		t.Fatalf("BindLocalSocket A: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove A's socket: %v", err)
	}
	b, err := BindLocalSocket(path, 0o600)
	if err != nil {
		t.Fatalf("BindLocalSocket B: %v", err)
	}
	defer func() { _ = b.Close(true) }()

	if err := a.Close(true); err != nil {
		t.Fatalf("Close A: %v", err)
	}
	if !b.Present() {
		t.Fatal("B's socket disappeared after A was closed")
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial after closing A: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close dialed connection: %v", err)
	}
}

func TestLocalSocketPresent(t *testing.T) {
	path := localSocketTestPath(t)
	sock, err := BindLocalSocket(path, 0o600)
	if err != nil {
		t.Fatalf("BindLocalSocket: %v", err)
	}
	if !sock.Present() {
		t.Fatal("Present() = false right after binding")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove socket: %v", err)
	}
	if sock.Present() {
		t.Fatal("Present() = true after the socket file was removed")
	}
	if err := sock.Close(false); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sock.Close(true); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path still exists after Close(true) with no socket present: %v", err)
	}
}
