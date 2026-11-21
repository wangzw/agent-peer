// Command check-release-notes checks that the release notes extracted from
// CHANGELOG.md become the body of the release that goreleaser publishes.
//
// goreleaser reads --release-notes only inside its changelog pipe. When that
// pipe is disabled, the flag is ignored and the published body is empty. A
// snapshot run cannot detect this: goreleaser skips the changelog pipe in
// snapshot mode whatever the configuration says. This command therefore runs a
// real release against an in-process fake GitHub API, captures the body of the
// release creation request, and compares it with the output of
// scripts/release-notes. goreleaser's body template appends one newline, which
// the comparison removes before comparing. The build and archive stages still
// run, resolving modules through the usual Go module proxy, but no SCM API is
// contacted.
//
// The same release uploads its assets to the fake API, so the command also
// checks that release.extra_files attaches scripts/remote-install.sh as
// install.sh and that the uploaded bytes equal the source file. That asset is
// what releases/latest/download/install.sh serves to the one-line installer.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

func main() {
	os.Exit(run())
}

func run() int {
	version := flag.String("version", "v0.1.0", "version to check; its section in CHANGELOG.md must be released")
	goreleaser := flag.String("goreleaser", "goreleaser", "goreleaser binary")
	bodyOut := flag.String("body-out", "", "write the published release body to this file")
	config := flag.String("config", ".goreleaser.yaml", "goreleaser configuration to check")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "check-release-notes: unexpected arguments: %s\n",
			strings.Join(flag.Args(), " "))
		return 2
	}

	bin, err := exec.LookPath(*goreleaser)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: goreleaser not found: %v\n", err)
		return 1
	}

	want, err := extractNotes(*version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
		return 1
	}

	server, err := startFakeGitHub(tagFor(*version))
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: starting the fake GitHub API: %v\n", err)
		return 1
	}
	defer server.close()

	dir, err := os.MkdirTemp("", "check-release-notes-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	notesPath := filepath.Join(dir, "release-notes.md")
	if err := os.WriteFile(notesPath, want, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
		return 1
	}
	configPath := filepath.Join(dir, "goreleaser.yaml")
	if err := writeCheckConfig(*config, configPath, server.url(), filepath.Join(dir, "dist")); err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
		return 1
	}

	output, err := runGoreleaser(bin, configPath, notesPath, *version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: goreleaser failed: %v\n%s", err, output)
		return 1
	}

	got, recorded := server.publishedBody()
	if !recorded {
		fmt.Fprintf(os.Stderr, "check-release-notes: goreleaser did not create a release; "+
			"the fake GitHub API received no release creation request\n%s", output)
		return 1
	}
	if *bodyOut != "" {
		if err := os.WriteFile(*bodyOut, []byte(got), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
			return 1
		}
	}
	status := report(string(want), got)
	if err := checkInstallAsset(server); err != nil {
		fmt.Fprintf(os.Stderr, "check-release-notes: %v\n", err)
		status = 1
	}
	return status
}

// extractNotes runs the release notes extractor and returns its output.
func extractNotes(version string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./scripts/release-notes", "-version", version)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("scripts/release-notes failed: %w\n%s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// writeCheckConfig copies the project configuration and points it at the fake
// GitHub API and a dist directory inside dir. Keeping dist out of the
// repository keeps --clean from touching a developer's local dist. The release
// block is kept as written: the publish stage must create the release, whose
// body is one thing under test, and upload its assets, install.sh among them,
// to the fake API.
func writeCheckConfig(source, path, apiURL, distDir string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	config := string(raw) +
		fmt.Sprintf("\ndist: %s\ngithub_urls:\n  api: %s/\n  upload: %s/\n", distDir, apiURL, apiURL)
	return os.WriteFile(path, []byte(config), 0o644)
}

// runGoreleaser performs a release with validation and announcements skipped,
// so HEAD does not need to be tagged. GORELEASER_CURRENT_TAG supplies the
// version. The publish stage stays enabled because creating the release is the
// behavior under test.
func runGoreleaser(bin, configPath, notesPath, version string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tag := tagFor(version)
	cmd := exec.CommandContext(ctx, bin,
		"release",
		"--clean",
		"--config", configPath,
		"--release-notes", notesPath,
		"--skip=validate,announce",
		"--timeout=10m",
	)
	cmd.Env = append(withoutEnv(os.Environ(),
		"GITHUB_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN", "GORELEASER_FORCE_TOKEN", "GORELEASER_CURRENT_TAG"),
		"GITHUB_TOKEN=check-release-notes",
		"GORELEASER_CURRENT_TAG="+tag,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

// tagFor returns version as a release tag.
func tagFor(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// withoutEnv returns env without the named variables, so a value this command
// sets cannot be shadowed by a duplicate inherited entry.
func withoutEnv(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, n := range names {
			if name == n {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

// report compares the published body with the extracted notes. goreleaser's
// body template appends exactly one newline to the notes, so that one byte is
// removed before comparing; every other byte must match.
func report(want, got string) int {
	gotBody := strings.TrimSuffix(got, "\n")
	if gotBody == want {
		fmt.Printf("check-release-notes: the published release body matches scripts/release-notes "+
			"(%d bytes; goreleaser adds one trailing newline); no generated changelog entries\n", len(want))
		return 0
	}
	fmt.Fprintln(os.Stderr, "check-release-notes: the published release body does not match scripts/release-notes")
	if strings.TrimSpace(got) == "" {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "goreleaser reads --release-notes only inside the changelog pipe. A blank body")
		fmt.Fprintln(os.Stderr, "means that pipe was skipped, so the release notes file was never read.")
	}
	fmt.Fprintf(os.Stderr, "expected: %d bytes from scripts/release-notes\n", len(want))
	fmt.Fprintf(os.Stderr, "actual:   %d bytes\n", len(got))
	if gotBody != "" {
		i := firstDifference(want, gotBody)
		fmt.Fprintf(os.Stderr, "first difference at byte %d: expected %q, got %q\n",
			i, snippet(want, i), snippet(gotBody, i))
	}
	return 1
}

func firstDifference(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

func snippet(s string, i int) string {
	end := min(i+40, len(s))
	if i >= len(s) {
		return "<end of body>"
	}
	return s[i:end]
}

const (
	// installAssetName is the release asset name the one-line installer is
	// fetched from: releases/latest/download/install.sh.
	installAssetName = "install.sh"
	// installScriptPath is the source release.extra_files attaches under that
	// name.
	installScriptPath = "scripts/remote-install.sh"
)

// checkInstallAsset checks that the release uploaded scripts/remote-install.sh
// as install.sh and that the uploaded bytes are exactly the file's bytes.
func checkInstallAsset(f *fakeGitHub) error {
	want, err := os.ReadFile(installScriptPath)
	if err != nil {
		return fmt.Errorf("reading %s, the source of the %s release asset: %w",
			installScriptPath, installAssetName, err)
	}

	assets := f.uploadedAssets()
	var matches []uploadedAsset
	var names []string
	for _, a := range assets {
		names = append(names, a.name)
		if a.name == installAssetName {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		uploaded := "none"
		if len(names) > 0 {
			uploaded = strings.Join(names, ", ")
		}
		return fmt.Errorf("the release has no %s asset; it uploaded: %s. "+
			"release.extra_files in .goreleaser.yaml must attach %s as %s",
			installAssetName, uploaded, installScriptPath, installAssetName)
	}
	if len(matches) > 1 {
		return fmt.Errorf("the release uploaded %s %d times; it must be attached once",
			installAssetName, len(matches))
	}

	got := matches[0].data
	if !bytes.Equal(got, want) {
		i := firstDifference(string(want), string(got))
		return fmt.Errorf("the release asset %s does not match %s: "+
			"expected %d bytes, got %d; first difference at byte %d: expected %q, got %q",
			installAssetName, installScriptPath, len(want), len(got), i,
			snippet(string(want), i), snippet(string(got), i))
	}
	fmt.Printf("check-release-notes: the release asset %s matches %s (%d bytes)\n",
		installAssetName, installScriptPath, len(want))
	return nil
}

// uploadedAsset is one asset goreleaser POSTed to the fake upload endpoint.
type uploadedAsset struct {
	name string
	data []byte
}

// fakeGitHub serves the few GitHub API responses goreleaser needs for a
// release and records the created body and the uploaded assets.
type fakeGitHub struct {
	listener net.Listener
	server   *http.Server
	tag      string

	mu     sync.Mutex
	body   string
	posted bool
	assets []uploadedAsset
}

func startFakeGitHub(tag string) (*fakeGitHub, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &fakeGitHub{listener: listener, tag: tag}
	f.server = &http.Server{Handler: http.HandlerFunc(f.handle), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = f.server.Serve(listener) }()
	return f, nil
}

func (f *fakeGitHub) url() string {
	return "http://" + f.listener.Addr().String()
}

func (f *fakeGitHub) close() {
	_ = f.server.Close()
}

func (f *fakeGitHub) publishedBody() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.posted
}

// uploadedAssets returns a copy of the assets uploaded so far. goreleaser
// uploads assets concurrently, so the copy is made under the lock.
func (f *fakeGitHub) uploadedAssets() []uploadedAsset {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.assets)
}

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/rate_limit"):
		reset := time.Now().Add(time.Hour).Unix()
		writeJSON(w, http.StatusOK, map[string]any{
			"resources": map[string]any{
				"core": map[string]any{"limit": 5000, "remaining": 4999, "reset": reset},
			},
			"rate": map[string]any{"limit": 5000, "remaining": 4999, "reset": reset},
		})
	case r.Method == http.MethodGet && strings.Contains(path, "/releases/tags/"):
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/releases"):
		var request struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
			return
		}
		f.mu.Lock()
		f.body = request.Body
		f.posted = true
		f.mu.Unlock()
		f.writeRelease(w)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/assets"):
		f.handleAssetUpload(w, r)
	case r.Method == http.MethodPatch && strings.Contains(path, "/releases/"):
		f.writeRelease(w)
	case r.Method == http.MethodGet:
		// Repository metadata for the release preflight check.
		writeJSON(w, http.StatusOK, map[string]any{
			"id": 1, "name": "peers", "full_name": "wangzw/peers",
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

// handleAssetUpload records one release asset upload. The asset name comes
// from the ?name= query parameter that go-github adds from UploadOptions.Name;
// the body is the file content.
func (f *fakeGitHub) handleAssetUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	f.mu.Lock()
	f.assets = append(f.assets, uploadedAsset{name: name, data: data})
	id := len(f.assets)
	f.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                   id,
		"name":                 name,
		"browser_download_url": fmt.Sprintf("%s/download/%d", f.url(), id),
	})
}

func (f *fakeGitHub) writeRelease(w http.ResponseWriter) {
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         1,
		"tag_name":   f.tag,
		"name":       f.tag,
		"html_url":   f.url() + "/release/1",
		"upload_url": f.url() + "/upload{?name,label}",
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
