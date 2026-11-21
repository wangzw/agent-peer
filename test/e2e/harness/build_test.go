package harness

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// TestImageTagCarriesAccountAndOwner fixes the image tag format: it names the
// peer account's uid and gid and the process that built the image. A cleanup
// relies on the owner marker to remove only images of runs that are gone.
func TestImageTagCarriesAccountAndOwner(t *testing.T) {
	current := currentOwner()
	tag := imageReference("0123456789ab", 1234, 5678, current)
	if !strings.HasPrefix(tag, "peers-e2e:ape2e-") {
		t.Fatalf("imageTag = %q, want the peers-e2e repository and owner marker", tag)
	}
	if !strings.Contains(tag, "-u1234-g5678-") {
		t.Fatalf("imageTag = %q, want the uid and gid in the tag", tag)
	}
	if !strings.HasSuffix(tag, "-0123456789ab") {
		t.Fatalf("imageTag = %q, want the content digest at the end", tag)
	}
	if imageTagOwnerGone(tag) {
		t.Fatalf("imageTagOwnerGone(%q) = true; the owner is this live process", tag)
	}

	dead := imageReference("0123456789ab", 1234, 5678, owner{pid: 999999999, start: 1, ns: current.ns})
	if !imageTagOwnerGone(dead) {
		t.Fatalf("imageTagOwnerGone(%q) = false; want the dead owner reported", dead)
	}
	if imageTagOwnerGone("peers-e2e:0123456789ab") {
		t.Fatalf("a tag without an owner marker was reported as removable")
	}
}

func TestBuildBinaryCachesAndInjectsVersion(t *testing.T) {
	pkg := "./test/e2e/harness/testdata/hello"

	first := BuildPackage(t, pkg, runtime.GOOS, runtime.GOARCH)
	second := BuildPackage(t, pkg, runtime.GOOS, runtime.GOARCH)
	if first != second {
		t.Fatalf("BuildPackage returned %q then %q for the same parameters", first, second)
	}

	out, err := exec.Command(first).Output()
	if err != nil {
		t.Fatalf("running %s: %v", first, err)
	}
	if got := strings.TrimSpace(string(out)); got != "e2e" {
		t.Fatalf("%s printed %q, want %q", first, got, "e2e")
	}

	bin := BuildBinary(t, runtime.GOOS, runtime.GOARCH)
	version, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("running %s --version: %v", bin, err)
	}
	if got := strings.TrimSpace(string(version)); got != "peers e2e" {
		t.Fatalf("%s --version printed %q, want %q", bin, got, "peers e2e")
	}

	// With go removed from PATH the cached results must still be returned.
	// A second build would fail instead.
	t.Setenv("PATH", t.TempDir())
	if again := BuildPackage(t, pkg, runtime.GOOS, runtime.GOARCH); again != first {
		t.Fatalf("cached BuildPackage returned %q, want %q", again, first)
	}
	if again := BuildBinary(t, runtime.GOOS, runtime.GOARCH); again != bin {
		t.Fatalf("cached BuildBinary returned %q, want %q", again, bin)
	}
}

// TestBuildBinaryVersionStampsTheNamedVersion checks that a caller can build
// the same package at several versions in one process: each version lands in
// its own output and reports itself, while the default stays e2e.
func TestBuildBinaryVersionStampsTheNamedVersion(t *testing.T) {
	old := BuildBinaryVersion(t, "0.1.0", runtime.GOOS, runtime.GOARCH, "peers_testenv")
	next := BuildBinaryVersion(t, "0.1.2", runtime.GOOS, runtime.GOARCH, "peers_testenv")
	if old == next {
		t.Fatalf("BuildBinaryVersion returned the same path %q for two versions", old)
	}
	for _, tc := range []struct{ path, want string }{
		{old, "peers 0.1.0"},
		{next, "peers 0.1.2"},
	} {
		out, err := exec.Command(tc.path, "--version").Output()
		if err != nil {
			t.Fatalf("running %s --version: %v", tc.path, err)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Fatalf("%s --version printed %q, want %q", tc.path, got, tc.want)
		}
	}

	// The default build is still a non-release version.
	if got := DefaultVersion; got != "e2e" {
		t.Fatalf("DefaultVersion = %q, want %q", got, "e2e")
	}
}
