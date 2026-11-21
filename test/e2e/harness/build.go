// Package harness provides the building blocks shared by the end-to-end
// tests: building product binaries, waiting for asynchronous events, and
// composing Broker configuration fragments.
package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildEntry memoises one go build invocation. once runs the build exactly
// once even when several callers ask for the same parameters.
type buildEntry struct {
	once sync.Once
	path string
	err  error
}

var (
	buildMu    sync.Mutex
	buildCache = map[string]*buildEntry{}

	buildDirOnce sync.Once
	buildDir     string
	buildDirErr  error

	modRootOnce sync.Once
	modRoot     string
	modRootErr  error
)

// DefaultVersion is the version stamped into binaries the harness builds when
// the caller does not name one. It is not a release version, so a build that
// checks for updates treats it as a non-release build.
const DefaultVersion = "e2e"

// BuildBinary builds ./cmd/peers for goos/goarch with the default version
// and returns the path of the resulting binary. The same parameters are built
// only once per test process.
func BuildBinary(tb testing.TB, goos, goarch string, tags ...string) string {
	tb.Helper()
	return BuildBinaryVersion(tb, DefaultVersion, goos, goarch, tags...)
}

// BuildBinaryVersion is BuildBinary with an explicit version stamped into
// main.version. Tests that exercise the release version, such as the update
// scenario, name the version they want here.
func BuildBinaryVersion(tb testing.TB, version, goos, goarch string, tags ...string) string {
	tb.Helper()
	return build(tb, "./cmd/peers", version, goos, goarch, tags)
}

// BuildBinaryVersionOverlay is BuildBinaryVersion with a Go build overlay file
// that substitutes source files for this build only. A test uses it to produce
// a binary whose behavior differs from the tree without changing the tree; the
// overlay is a JSON file in the form go build's -overlay flag accepts.
func BuildBinaryVersionOverlay(tb testing.TB, version, goos, goarch, overlay string, tags ...string) string {
	tb.Helper()
	return buildWithOverlay(tb, "./cmd/peers", version, goos, goarch, tags, overlay)
}

// BuildPackage builds pkg for goos/goarch and returns the path of the result.
// Like BuildBinary, the same parameters are built only once per test process.
func BuildPackage(tb testing.TB, pkg, goos, goarch string, tags ...string) string {
	tb.Helper()
	return build(tb, pkg, DefaultVersion, goos, goarch, tags)
}

// ModuleRoot returns the root directory of the Go module under test. It lets a
// test name a source file an overlay replaces.
func ModuleRoot(tb testing.TB) string {
	tb.Helper()
	root, err := moduleRoot()
	if err != nil {
		tb.Fatalf("harness: %v", err)
	}
	return root
}

func build(tb testing.TB, pkg, version, goos, goarch string, tags []string) string {
	return buildWithOverlay(tb, pkg, version, goos, goarch, tags, "")
}

func buildWithOverlay(tb testing.TB, pkg, version, goos, goarch string, tags []string, overlay string) string {
	key := strings.Join([]string{pkg, version, goos, goarch, strings.Join(tags, ","), overlay}, "\x00")

	buildMu.Lock()
	entry, ok := buildCache[key]
	if !ok {
		entry = &buildEntry{}
		buildCache[key] = entry
	}
	buildMu.Unlock()

	entry.once.Do(func() {
		entry.path, entry.err = goBuild(pkg, version, goos, goarch, tags, overlay)
	})
	if entry.err != nil {
		tb.Fatalf("harness: building %s for %s/%s: %v", pkg, goos, goarch, entry.err)
	}
	return entry.path
}

func goBuild(pkg, version, goos, goarch string, tags []string, overlay string) (string, error) {
	dir, err := buildDirectory()
	if err != nil {
		return "", err
	}
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}

	out := filepath.Join(dir, outputName(pkg, version, goos, goarch, tags))
	if goos == "windows" && !strings.HasSuffix(out, ".exe") {
		out += ".exe"
	}

	args := []string{"build", "-trimpath", "-ldflags", "-X main.version=" + version}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	args = append(args, "-o", out, pkg)

	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build %s: %w\n%s", pkg, err, strings.TrimSpace(string(combined)))
	}
	return out, nil
}

// outputName derives a stable file name from the build parameters so that
// binaries never overwrite each other inside the shared build directory.
func outputName(pkg, version, goos, goarch string, tags []string) string {
	tagPart := "notags"
	if len(tags) > 0 {
		tagPart = strings.Join(tags, "-")
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		sanitize(filepath.Base(filepath.Clean(pkg))), sanitize(version), goos, goarch, sanitize(tagPart))
}

// sanitize keeps the characters that are safe in a file name and replaces the
// rest, so a package path or tag list can be used verbatim in the output name.
func sanitize(s string) string {
	if s == "" || s == "." {
		return "pkg"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func buildDirectory() (string, error) {
	buildDirOnce.Do(func() {
		buildDir, buildDirErr = os.MkdirTemp("", "ap-e2e-build-")
	})
	return buildDir, buildDirErr
}

func moduleRoot() (string, error) {
	modRootOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOMOD").Output()
		if err != nil {
			modRootErr = fmt.Errorf("go env GOMOD: %w", err)
			return
		}
		gomod := strings.TrimSpace(string(out))
		if gomod == "" || gomod == os.DevNull {
			modRootErr = errors.New("not inside a Go module")
			return
		}
		modRoot = filepath.Dir(gomod)
	})
	return modRoot, modRootErr
}
