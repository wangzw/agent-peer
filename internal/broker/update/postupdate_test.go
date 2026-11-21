package update

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// postUpdateScript returns a stand-in release binary. It answers the version
// preflight, records the arguments of the hidden post-update command in
// marker, writes notes to stderr, and exits with code.
func postUpdateScript(version, marker string, code int, notes string) []byte {
	stderr := ""
	if notes != "" {
		stderr = fmt.Sprintf("echo '%s' >&2\n", notes)
	}
	return []byte(fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "peers %s"
  exit 0
fi
printf '%%s\n' "$*" > '%s'
%s
exit %d
`, version, marker, stderr, code))
}

// postUpdateHangScript returns a stand-in release binary whose post-update
// command never finishes on its own. exec keeps sleep the direct child, so the
// timeout kills the process the test started.
func postUpdateHangScript(version string) []byte {
	return []byte(fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "peers %s"
  exit 0
fi
exec sleep 30
`, version))
}

// serveScript stages script as the release binary of version and returns the
// options that install it over target.
func serveScript(t *testing.T, target, version string, script []byte) ApplyOptions {
	t.Helper()
	archive := tarGz(t, tarEntry{name: binaryName, body: script, mode: 0o755})
	srv := newApplyServer(t, version, writeBytes(archive), checksumsFor(archive, testArchiveName(version)))
	return testApplyOptions(srv, target, version)
}

// postUpdateArgs reads the arguments the installed binary received.
func postUpdateArgs(t *testing.T, marker string) string {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the post-update step did not run: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// assertNoRollbackFiles checks that Install removed its rollback copy.
func assertNoRollbackFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the target directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".peers-rollback-") {
			t.Errorf("rollback file %q was left behind", entry.Name())
		}
	}
}

func TestInstallRunsPostUpdate(t *testing.T) {
	const version = "0.2.0"
	original := []byte("original binary")
	target, _ := newTarget(t, original, 0o750)
	marker := filepath.Join(filepath.Dir(target), "post-update-args")
	script := postUpdateScript(version, marker, 0, "")

	if err := Install(context.Background(), serveScript(t, target, version, script), "user"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if got, want := postUpdateArgs(t, marker), "internal post-update --level user"; got != want {
		t.Errorf("post-update arguments = %q, want %q", got, want)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the installed binary: %v", err)
	}
	if !bytes.Equal(got, script) {
		t.Errorf("target content = %q, want the installed binary", got)
	}
	assertNoRollbackFiles(t, filepath.Dir(target))
}

func TestInstallPostUpdateNotNeeded(t *testing.T) {
	const version = "0.2.0"
	original := []byte("original binary")
	target, _ := newTarget(t, original, 0o750)
	marker := filepath.Join(filepath.Dir(target), "post-update-args")
	script := postUpdateScript(version, marker, postUpdateExitNotNeeded, "")

	if err := Install(context.Background(), serveScript(t, target, version, script), "user"); err != nil {
		t.Fatalf("Install() error = %v, want exit code 2 to continue", err)
	}

	// The step ran; only its exit code says there was nothing to do.
	if got, want := postUpdateArgs(t, marker), "internal post-update --level user"; got != want {
		t.Errorf("post-update arguments = %q, want %q", got, want)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the installed binary: %v", err)
	}
	if !bytes.Equal(got, script) {
		t.Errorf("target content = %q, want the installed binary", got)
	}
	assertNoRollbackFiles(t, filepath.Dir(target))
}

func TestInstallPostUpdateFailureRollsBack(t *testing.T) {
	const version = "0.2.0"
	original := []byte("original binary")
	target, before := newTarget(t, original, 0o750)
	marker := filepath.Join(filepath.Dir(target), "post-update-args")
	script := postUpdateScript(version, marker, 3, "cannot rewrite the service unit")

	err := Install(context.Background(), serveScript(t, target, version, script), "user")
	if err == nil {
		t.Fatal("Install() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "post-update") {
		t.Errorf("error %q does not name the post-update step", err)
	}
	if !strings.Contains(err.Error(), "cannot rewrite the service unit") {
		t.Errorf("error %q does not carry the step's reason", err)
	}
	if !strings.Contains(err.Error(), "previous binary was restored") {
		t.Errorf("error %q does not say the binary was restored", err)
	}
	if got := postUpdateArgs(t, marker); got == "" {
		t.Error("the post-update step did not run")
	}
	assertUnchanged(t, target, original, before)
	assertNoRollbackFiles(t, filepath.Dir(target))
}

func TestInstallPostUpdateTimeoutRollsBack(t *testing.T) {
	const version = "0.2.0"
	original := []byte("original binary")
	target, before := newTarget(t, original, 0o750)
	script := postUpdateHangScript(version)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := Install(ctx, serveScript(t, target, version, script), "system")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Install() = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error %q does not report a timeout", err)
	}
	if !strings.Contains(err.Error(), "previous binary was restored") {
		t.Errorf("error %q does not say the binary was restored", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("Install() took %s, want it to stop near the deadline", elapsed)
	}
	assertUnchanged(t, target, original, before)
	assertNoRollbackFiles(t, filepath.Dir(target))
}
