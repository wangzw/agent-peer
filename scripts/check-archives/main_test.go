package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testReadme stands in for scripts/archive-README.md.
var testReadme = []byte("peers release archive README\n")

// testInventory builds a repository skeleton and returns the inventory and
// archive README that expectedFiles derives from it. The tests use the
// production expectation rather than a hand-written copy, so an entry removed
// from expectedFiles is a failure here too.
func testInventory(t *testing.T) (map[string]struct{}, []byte) {
	t.Helper()

	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	thirdParty := filepath.Join(root, "THIRD_PARTY_LICENSES", "claude-code-socket-transport")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatalf("creating %s: %v", scripts, err)
	}
	if err := os.MkdirAll(thirdParty, 0o755); err != nil {
		t.Fatalf("creating %s: %v", thirdParty, err)
	}
	for path, data := range map[string][]byte{
		filepath.Join(scripts, "install.sh"):        []byte("script"),
		filepath.Join(scripts, "archive-README.md"): testReadme,
		filepath.Join(thirdParty, "LICENSE"):        []byte("license"),
	} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	want, err := expectedFiles(root)
	if err != nil {
		t.Fatalf("expectedFiles: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(scripts, "archive-README.md"))
	if err != nil {
		t.Fatalf("reading the archive README: %v", err)
	}
	return want, readme
}

// testEntry is one file written into a test archive.
type testEntry struct {
	name string
	mode int64
	data []byte
}

// goodEntries returns a complete, valid archive inventory with the README
// exactly matching testReadme.
func goodEntries() []testEntry {
	return []testEntry{
		{name: "peers", mode: 0o755, data: []byte("binary")},
		{name: "install.sh", mode: 0o755, data: []byte("script")},
		{name: "README.md", mode: 0o644, data: testReadme},
		{name: "LICENSE", mode: 0o644, data: []byte("license")},
		{name: "third_party/claude-code-socket-transport/LICENSE", mode: 0o644, data: []byte("license")},
		{name: "THIRD_PARTY_LICENSES/claude-code-socket-transport/LICENSE", mode: 0o644, data: []byte("license")},
	}
}

// writeTestArchive writes entries as a gzipped tar at path.
func writeTestArchive(t *testing.T, path string, entries []testEntry) {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("writing the header for %s: %v", entry.name, err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatalf("writing %s: %v", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing the tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing the gzip writer: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// verifyTestArchive runs the production verifier with the inventory derived
// from testInventory.
func verifyTestArchive(t *testing.T, path string) []string {
	t.Helper()

	want, readme := testInventory(t)
	failures, err := verifyArchive(path, want, readme)
	if err != nil {
		t.Fatalf("verifyArchive: %v", err)
	}
	return failures
}

// mustReport fails unless one failure contains want.
func mustReport(t *testing.T, failures []string, want string) {
	t.Helper()
	for _, failure := range failures {
		if strings.Contains(failure, want) {
			return
		}
	}
	t.Fatalf("failures = %q, want one containing %q", failures, want)
}

// TestVerifyArchiveAcceptsCompleteArchive keeps the test fixture honest: the
// inventory used by the failure cases must pass when nothing is altered.
func TestVerifyArchiveAcceptsCompleteArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers_test.tar.gz")
	writeTestArchive(t, path, goodEntries())

	failures := verifyTestArchive(t, path)
	if len(failures) != 0 {
		t.Fatalf("failures = %q, want none", failures)
	}
}

func TestVerifyArchiveRejectsUnexpectedFile(t *testing.T) {
	entries := append(goodEntries(), testEntry{name: "extra.txt", mode: 0o644, data: []byte("extra")})
	path := filepath.Join(t.TempDir(), "peers_test.tar.gz")
	writeTestArchive(t, path, entries)

	failures := verifyTestArchive(t, path)
	mustReport(t, failures, "unexpected extra.txt")
}

func TestVerifyArchiveRejectsMissingFile(t *testing.T) {
	var entries []testEntry
	for _, entry := range goodEntries() {
		if entry.name != "install.sh" {
			entries = append(entries, entry)
		}
	}
	path := filepath.Join(t.TempDir(), "peers_test.tar.gz")
	writeTestArchive(t, path, entries)

	failures := verifyTestArchive(t, path)
	mustReport(t, failures, "missing install.sh")
}

func TestVerifyArchiveRejectsWrongMode(t *testing.T) {
	entries := goodEntries()
	for i := range entries {
		if entries[i].name == "install.sh" {
			entries[i].mode = 0o644
		}
	}
	path := filepath.Join(t.TempDir(), "peers_test.tar.gz")
	writeTestArchive(t, path, entries)

	failures := verifyTestArchive(t, path)
	mustReport(t, failures, "install.sh has mode 0644, want 0755")
}

func TestVerifyArchiveRejectsChangedReadme(t *testing.T) {
	entries := goodEntries()
	changed := append([]byte{}, testReadme...)
	changed[len(changed)-1] ^= 0x01
	for i := range entries {
		if entries[i].name == "README.md" {
			entries[i].data = changed
		}
	}
	path := filepath.Join(t.TempDir(), "peers_test.tar.gz")
	writeTestArchive(t, path, entries)

	failures := verifyTestArchive(t, path)
	mustReport(t, failures, "README.md differs from scripts/archive-README.md")
}

func TestVerifyChecksumsRejectsWrongDigest(t *testing.T) {
	dir := t.TempDir()
	archive := "peers_test.tar.gz"
	path := filepath.Join(dir, archive)
	if err := os.WriteFile(path, []byte("archive bytes"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"),
		[]byte(strings.Repeat("0", 64)+"  "+archive+"\n"), 0o644); err != nil {
		t.Fatalf("writing checksums.txt: %v", err)
	}

	failures := verifyChecksums(dir, []string{archive})
	mustReport(t, failures, "has digest")
}

func TestVerifyChecksumsAcceptsMatchingDigest(t *testing.T) {
	dir := t.TempDir()
	archive := "peers_test.tar.gz"
	data := []byte("archive bytes")
	path := filepath.Join(dir, archive)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"),
		[]byte(hex.EncodeToString(sum[:])+"  "+archive+"\n"), 0o644); err != nil {
		t.Fatalf("writing checksums.txt: %v", err)
	}

	failures := verifyChecksums(dir, []string{archive})
	if len(failures) != 0 {
		t.Fatalf("failures = %q, want none", failures)
	}
}
