// Command check-archives builds a goreleaser snapshot and checks that every
// release archive contains exactly the files a release ships: the binary, the
// install script, the archive README, the project licenses and the third-party
// license inventory. The binary's test hook string is covered by the goreleaser
// build hook, which fails the build before an archive is written, so this
// command does not repeat that check.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// archivesPerRelease is the number of platform archives goreleaser builds:
// darwin and linux, each on amd64 and arm64.
const archivesPerRelease = 4

// executableMode is the permission bits the binary and the install script must
// carry inside every archive.
const executableMode = 0o755

func main() {
	os.Exit(run())
}

func run() int {
	goreleaser := flag.String("goreleaser", "goreleaser", "goreleaser binary")
	config := flag.String("config", ".goreleaser.yaml", "goreleaser configuration to build")
	timeout := flag.Duration("timeout", 15*time.Minute, "timeout for the goreleaser snapshot")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "check-archives: unexpected arguments: %s\n",
			strings.Join(flag.Args(), " "))
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-archives: %v\n", err)
		return 1
	}
	if !filepath.IsAbs(*config) {
		*config = filepath.Join(root, *config)
	}

	want, err := expectedFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-archives: %v\n", err)
		return 1
	}
	wantReadme, err := os.ReadFile(filepath.Join(root, "scripts", "archive-README.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-archives: reading the archive README: %v\n", err)
		return 1
	}

	distDir, err := buildSnapshot(*goreleaser, *config, root, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-archives: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(distDir)) }()

	failures, err := verify(distDir, want, wantReadme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-archives: %v\n", err)
		return 1
	}
	for _, failure := range failures {
		fmt.Fprintf(os.Stderr, "check-archives: %s\n", failure)
	}
	if len(failures) > 0 {
		return 1
	}
	fmt.Fprintf(os.Stderr, "check-archives: %d archives verified\n", archivesPerRelease)
	return 0
}

// buildSnapshot runs goreleaser in root with a configuration that writes to a
// temporary dist directory, and returns that directory. The project
// configuration is copied rather than edited in place so the repository is left
// untouched.
func buildSnapshot(goreleaser, config, root string, timeout time.Duration) (string, error) {
	bin, err := exec.LookPath(goreleaser)
	if err != nil {
		return "", fmt.Errorf("goreleaser not found: %w", err)
	}

	tmp, err := os.MkdirTemp("", "check-archives-")
	if err != nil {
		return "", err
	}
	distDir := filepath.Join(tmp, "dist")
	configPath := filepath.Join(tmp, "goreleaser.yaml")
	if err := writeConfig(config, configPath, distDir); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "release", "--snapshot", "--clean", "-f", configPath)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tmp)
		if ctx.Err() != nil {
			return "", fmt.Errorf("goreleaser did not finish within %s: %w\n%s", timeout, err, output)
		}
		return "", fmt.Errorf("goreleaser failed: %w\n%s", err, output)
	}
	return distDir, nil
}

// distBlock matches a top-level dist key. The copied configuration must not
// have one, because a second dist key makes the YAML invalid.
var distBlock = regexp.MustCompile(`(?m)^dist:`)

// writeConfig copies the project configuration and points dist at distDir.
func writeConfig(source, path, distDir string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if distBlock.Match(raw) {
		return fmt.Errorf("%s already sets dist", source)
	}
	config := string(raw)
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	config += "\ndist: " + strconv.Quote(distDir) + "\n"
	return os.WriteFile(path, []byte(config), 0o644)
}

// expectedFiles returns the archive-relative paths every release archive must
// contain. The third-party license inventory is read from the repository so a
// module added to the inventory joins the expectation automatically.
func expectedFiles(root string) (map[string]struct{}, error) {
	want := map[string]struct{}{
		"peers":      {},
		"install.sh": {},
		"README.md":  {},
		"LICENSE":    {},
		"third_party/claude-code-socket-transport/LICENSE": {},
	}
	for _, name := range []string{"scripts/install.sh", "scripts/archive-README.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}

	inventory := filepath.Join(root, "THIRD_PARTY_LICENSES")
	err := filepath.WalkDir(inventory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(inventory, path)
		if err != nil {
			return err
		}
		want["THIRD_PARTY_LICENSES/"+filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the third-party license inventory: %w", err)
	}
	return want, nil
}

// verify checks every archive in distDir and returns the failures it found. A
// returned error means the archives themselves could not be read.
func verify(distDir string, want map[string]struct{}, wantReadme []byte) ([]string, error) {
	archives, err := listArchives(distDir)
	if err != nil {
		return nil, err
	}
	if len(archives) != archivesPerRelease {
		return nil, fmt.Errorf("expected %d archives in %s, found %d: %s",
			archivesPerRelease, distDir, len(archives), strings.Join(archives, ", "))
	}

	var failures []string
	failures = append(failures, verifyChecksums(distDir, archives)...)
	for _, archive := range archives {
		archiveFailures, err := verifyArchive(filepath.Join(distDir, archive), want, wantReadme)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", archive, err)
		}
		failures = append(failures, archiveFailures...)
		if len(archiveFailures) == 0 {
			fmt.Fprintf(os.Stderr, "check-archives: %s: ok (%d files)\n", archive, len(want))
		}
	}
	return failures, nil
}

// listArchives returns the archive file names in distDir, sorted.
func listArchives(distDir string) ([]string, error) {
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return nil, err
	}
	var archives []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tar.gz") {
			archives = append(archives, entry.Name())
		}
	}
	sort.Strings(archives)
	return archives, nil
}

// verifyChecksums checks that checksums.txt covers exactly the archives and
// that every digest matches the archive on disk.
func verifyChecksums(distDir string, archives []string) []string {
	raw, err := os.ReadFile(filepath.Join(distDir, "checksums.txt"))
	if err != nil {
		return []string{fmt.Sprintf("reading checksums.txt: %v", err)}
	}

	sums := map[string]string{}
	var failures []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			failures = append(failures, fmt.Sprintf("checksums.txt: malformed line %q", line))
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	for _, archive := range archives {
		want, ok := sums[archive]
		if !ok {
			failures = append(failures, fmt.Sprintf("checksums.txt: no digest for %s", archive))
			continue
		}
		got, err := fileSHA256(filepath.Join(distDir, archive))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", archive, err))
			continue
		}
		if !strings.EqualFold(got, want) {
			failures = append(failures, fmt.Sprintf("checksums.txt: %s has digest %s, want %s", archive, got, want))
		}
	}
	archiveSet := fileSet(archives)
	for name := range sums {
		if _, ok := archiveSet[name]; !ok {
			failures = append(failures, fmt.Sprintf("checksums.txt: digest for unknown file %s", name))
		}
	}
	return failures
}

func fileSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// archiveEntry is one non-directory header read from an archive.
type archiveEntry struct {
	mode    int64
	regular bool
}

// verifyArchive compares one archive against the expected files and returns the
// failures it found.
func verifyArchive(path string, want map[string]struct{}, wantReadme []byte) ([]string, error) {
	files, readme, err := readArchive(path)
	if err != nil {
		return nil, err
	}

	var failures []string
	got := make(map[string]struct{}, len(files))
	for name := range files {
		got[name] = struct{}{}
	}
	for _, name := range missing(want, got) {
		failures = append(failures, fmt.Sprintf("%s: missing %s", filepath.Base(path), name))
	}
	for _, name := range missing(got, want) {
		failures = append(failures, fmt.Sprintf("%s: unexpected %s", filepath.Base(path), name))
	}
	for _, name := range []string{"peers", "install.sh"} {
		entry, ok := files[name]
		if !ok {
			continue
		}
		if !entry.regular {
			failures = append(failures, fmt.Sprintf("%s: %s is not a regular file", filepath.Base(path), name))
			continue
		}
		if entry.mode != executableMode {
			failures = append(failures, fmt.Sprintf("%s: %s has mode %04o, want %04o",
				filepath.Base(path), name, entry.mode, executableMode))
		}
	}
	if readme == nil {
		failures = append(failures, fmt.Sprintf("%s: README.md could not be read", filepath.Base(path)))
	} else if !bytes.Equal(readme, wantReadme) {
		failures = append(failures, fmt.Sprintf("%s: README.md differs from scripts/archive-README.md",
			filepath.Base(path)))
	}
	return failures, nil
}

// readArchive returns the non-directory entries of the archive keyed by name,
// together with the contents of README.md.
func readArchive(path string) (map[string]archiveEntry, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = gz.Close() }()

	files := map[string]archiveEntry{}
	var readme []byte
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		files[name] = archiveEntry{mode: hdr.Mode & 0o777, regular: hdr.Typeflag == tar.TypeReg}
		if name == "README.md" {
			readme, err = io.ReadAll(tr)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return files, readme, nil
}

// missing returns the sorted keys of want that are absent from got.
func missing(want, got map[string]struct{}) []string {
	var out []string
	for name := range want {
		if _, ok := got[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
