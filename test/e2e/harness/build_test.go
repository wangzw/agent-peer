package harness

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

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
	if got := strings.TrimSpace(string(version)); got != "agent-peer e2e" {
		t.Fatalf("%s --version printed %q, want %q", bin, got, "agent-peer e2e")
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
