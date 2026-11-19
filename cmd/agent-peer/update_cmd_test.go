package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wangzw/agent-peer/internal/broker/update"
)

// countingTransport fails a test if the command reaches the network. The
// update command's GitHub calls are stubbed, so the transport must stay
// untouched, and counting it makes the no-download assertions direct.
type countingTransport struct {
	t     *testing.T
	calls int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	c.t.Errorf("the update command issued a network request")
	return nil, errors.New("unexpected network request")
}

// updateTestEnv installs the operating-system seams around a temporary target
// binary and restores them when the test ends.
type updateTestEnv struct {
	target    string
	transport *countingTransport
}

func newUpdateTestEnv(t *testing.T) *updateTestEnv {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-peer")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := &updateTestEnv{target: target, transport: &countingTransport{t: t}}

	previous := struct {
		executable   func() (string, error)
		evalSymlinks func(string) (string, error)
		euid         func() int
		newClient    func() *http.Client
		isTerminal   func(io.Reader) bool
		goos, goarch string
	}{
		updateExecutable, updateEvalSymlinks, updateEUID, updateNewClient,
		updateIsTerminal, updateGOOS, updateGOARCH,
	}
	updateExecutable = func() (string, error) { return target, nil }
	updateEvalSymlinks = func(path string) (string, error) { return path, nil }
	updateEUID = func() int { return 1000 }
	updateNewClient = func() *http.Client { return &http.Client{Transport: env.transport} }
	updateIsTerminal = func(io.Reader) bool { return true }
	updateGOOS = "linux"
	updateGOARCH = "amd64"
	t.Cleanup(func() {
		updateExecutable = previous.executable
		updateEvalSymlinks = previous.evalSymlinks
		updateEUID = previous.euid
		updateNewClient = previous.newClient
		updateIsTerminal = previous.isTerminal
		updateGOOS = previous.goos
		updateGOARCH = previous.goarch
	})
	return env
}

func assertBinaryUnchanged(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old binary" {
		t.Fatalf("target content = %q, want the original binary", got)
	}
}

func TestUpdateRejectsNonReleaseBuilds(t *testing.T) {
	for _, version := range []string{"dev", "e2e", "0.1.2-SNAPSHOT-abc123", "", "0.1", "0.1.0+build.5"} {
		t.Run(version, func(t *testing.T) {
			a, stdout, stderr, stub := newDispatchApp(version)
			if got := a.run(context.Background(), []string{"update"}); got != exitFailure {
				t.Fatalf("run(update) = %d, want %d", got, exitFailure)
			}
			if !strings.Contains(stderr.String(), "not a release build") {
				t.Fatalf("stderr = %q, want the non-release explanation", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if stub.latestCalls != 0 || len(stub.applyCalls) != 0 {
				t.Fatalf("latest calls = %d, apply calls = %d, want none", stub.latestCalls, len(stub.applyCalls))
			}
		})
	}
}

func TestUpdateFailsBeforeAnyDownloadWithoutWritePermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so this case cannot be reproduced as root")
	}
	env := newUpdateTestEnv(t)

	lockedDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(lockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(lockedDir, "agent-peer")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })

	updateExecutable = func() (string, error) { return target, nil }
	updateEvalSymlinks = func(path string) (string, error) { return path, nil }

	a, _, stderr, stub := newDispatchApp("0.1.1")
	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if stub.latestCalls != 0 {
		t.Fatalf("latest calls = %d, want none before the permission check passes", stub.latestCalls)
	}
	if len(stub.applyCalls) != 0 {
		t.Fatalf("apply calls = %d, want none", len(stub.applyCalls))
	}
	if env.transport.calls != 0 {
		t.Fatalf("network requests = %d, want none before the permission check passes", env.transport.calls)
	}
	if !strings.Contains(stderr.String(), "sudo agent-peer update") {
		t.Fatalf("stderr = %q, want the sudo hint", stderr.String())
	}
	assertBinaryUnchanged(t, target)
}

func TestUpdateRefusesNonTerminalInputWithoutYes(t *testing.T) {
	env := newUpdateTestEnv(t)
	updateIsTerminal = func(io.Reader) bool { return false }

	a, _, stderr, stub := newDispatchApp("0.1.1")
	stub.latestRelease = update.Release{Version: "0.1.2"}
	stub.newerResult = true
	a.deps = stub.deps()
	a.stdin = strings.NewReader("y\n")
	if got := a.run(context.Background(), []string{"update"}); got != exitFailure {
		t.Fatalf("run(update) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("stderr = %q, want it to offer --yes", stderr.String())
	}
	if stub.latestCalls != 1 || len(stub.applyCalls) != 0 {
		t.Fatalf("latest calls = %d, apply calls = %d, want one lookup and no apply", stub.latestCalls, len(stub.applyCalls))
	}
	if env.transport.calls != 0 {
		t.Fatalf("network requests = %d, want none", env.transport.calls)
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateReportsUpToDate(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease: update.Release{Version: "0.1.1", URL: "https://github.com/wangzw/agent-peer/releases/tag/v0.1.1"},
		newerResult:   false,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "agent-peer 0.1.1 is up to date\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if len(stub.applyCalls) != 0 || len(stub.restartCalls) != 0 {
		t.Fatalf("apply calls = %d, restart calls = %d, want none", len(stub.applyCalls), len(stub.restartCalls))
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateUserDeclines(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease: update.Release{Version: "0.1.2", URL: "https://example.test/release"},
		newerResult:   true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	a.stdin = strings.NewReader("n\n")

	if got := a.run(context.Background(), []string{"update"}); got != exitOK {
		t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Update canceled") {
		t.Fatalf("stdout = %q, want the cancellation line", stdout.String())
	}
	if len(stub.applyCalls) != 0 || len(stub.restartCalls) != 0 {
		t.Fatalf("apply calls = %d, restart calls = %d, want none", len(stub.applyCalls), len(stub.restartCalls))
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateNonTerminalUpToDateExitsZero(t *testing.T) {
	newUpdateTestEnv(t)
	updateIsTerminal = func(io.Reader) bool { return false }
	stub := &stubDeps{
		latestRelease: update.Release{Version: "0.1.1"},
		newerResult:   false,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	a.stdin = strings.NewReader("")

	if got := a.run(context.Background(), []string{"update"}); got != exitOK {
		t.Fatalf("run(update) with non-terminal input and the latest release = %d, want %d (stderr %q)",
			got, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is up to date") {
		t.Fatalf("stdout = %q, want the up-to-date line", stdout.String())
	}
	if strings.Contains(stderr.String(), "not a terminal") {
		t.Fatalf("stderr = %q, want no terminal refusal before the release is known", stderr.String())
	}
	if len(stub.applyCalls) != 0 {
		t.Fatalf("apply calls = %d, want none", len(stub.applyCalls))
	}
}

func TestUpdateConfirmsAndRestartsTheService(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease:    update.Release{Version: "0.1.2", URL: "https://example.test/release"},
		newerResult:      true,
		serviceRestarted: true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	a.stdin = strings.NewReader("yes\n")

	if got := a.run(context.Background(), []string{"update"}); got != exitOK {
		t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.applyCalls) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(stub.applyCalls))
	}
	got := stub.applyCalls[0]
	if got.Target != env.target || got.Version != "0.1.2" {
		t.Fatalf("ApplyOptions = %+v, want target %q and version 0.1.2", got, env.target)
	}
	if got.GOOS != "linux" || got.GOARCH != "amd64" {
		t.Fatalf("ApplyOptions platform = %s/%s, want linux/amd64", got.GOOS, got.GOARCH)
	}
	if got.API != "https://api.example" || got.Client == nil {
		t.Fatalf("ApplyOptions API = %q, client = %v, want the wired API and client", got.API, got.Client)
	}
	if len(stub.serviceOptionCalls) != 1 || stub.serviceOptionCalls[0] {
		t.Fatalf("service option calls = %v, want one user-level call", stub.serviceOptionCalls)
	}
	if len(stub.restartCalls) != 1 || stub.restartCalls[0].System {
		t.Fatalf("restart calls = %+v, want one user-level restart", stub.restartCalls)
	}
	out := stdout.String()
	if !strings.Contains(out, "Restarted the agent-peer service.") {
		t.Fatalf("stdout = %q, want the restart line", out)
	}
	if !strings.Contains(out, "reconnect with /mcp") {
		t.Fatalf("stdout = %q, want the session hint", out)
	}
	if !strings.Contains(out, "User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user.") {
		t.Fatalf("stdout = %q, want the shared-binary hint", out)
	}
}

func TestUpdateYesSkipsTheQuestion(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease:    update.Release{Version: "0.1.2"},
		newerResult:      true,
		serviceRestarted: true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	a.stdin = strings.NewReader("")

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.applyCalls) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(stub.applyCalls))
	}
	if strings.Contains(stdout.String(), "[y/N]") {
		t.Fatalf("stdout = %q, want no question with --yes", stdout.String())
	}
}

func TestUpdateRootUsesTheSystemServiceLevel(t *testing.T) {
	newUpdateTestEnv(t)
	updateEUID = func() int { return 0 }
	stub := &stubDeps{
		latestRelease:    update.Release{Version: "0.1.2"},
		newerResult:      true,
		serviceRestarted: true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if len(stub.serviceOptionCalls) != 1 || !stub.serviceOptionCalls[0] {
		t.Fatalf("service option calls = %v, want one system-level call", stub.serviceOptionCalls)
	}
	if len(stub.restartCalls) != 1 || !stub.restartCalls[0].System {
		t.Fatalf("restart calls = %+v, want one system-level restart", stub.restartCalls)
	}
	if !strings.Contains(stdout.String(), "User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user.") {
		t.Fatalf("stdout = %q, want the shared-binary hint after a system-level update", stdout.String())
	}
}

func TestUpdateHintsToRestartAnUnmanagedBroker(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease:    update.Release{Version: "0.1.2"},
		newerResult:      true,
		serviceRestarted: false,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "restart it to use the new version") {
		t.Fatalf("stdout = %q, want the manual restart hint", stdout.String())
	}
}

func TestUpdateApplyFailureLeavesTheBinary(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease: update.Release{Version: "0.1.2"},
		newerResult:   true,
		applyErr:      errors.New("checksum mismatch"),
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("stderr = %q, want the apply error", stderr.String())
	}
	if len(stub.restartCalls) != 0 {
		t.Fatalf("restart calls = %d, want none after a failed replacement", len(stub.restartCalls))
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateRestartFailureIsReported(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		latestRelease: update.Release{Version: "0.1.2"},
		newerResult:   true,
		restartErr:    errors.New("systemctl failed"),
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "restarting the service: systemctl failed") {
		t.Fatalf("stderr = %q, want the restart error", stderr.String())
	}
}

func TestUpdateQueriesTheLatestReleaseEndpoint(t *testing.T) {
	env := newUpdateTestEnv(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = fmt.Fprint(w, `{"tag_name":"v0.1.2","html_url":"https://example.test/release"}`)
	}))
	t.Cleanup(srv.Close)
	updateNewClient = func() *http.Client { return srv.Client() }

	// The release lookup runs against the fake API; Apply stays a test double,
	// so the command never downloads anything in this test.
	stub := &stubDeps{
		updateAPIBase: func() string { return srv.URL },
		updateLatest:  update.Latest,
		updateNewer:   update.Newer,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "/repos/wangzw/agent-peer/releases/latest"; len(paths) != 1 || paths[0] != want {
		t.Fatalf("requests = %q, want exactly %q", paths, want)
	}
	if len(stub.applyCalls) != 1 || stub.applyCalls[0].Version != "0.1.2" {
		t.Fatalf("apply calls = %+v, want one apply of 0.1.2", stub.applyCalls)
	}
	if !strings.Contains(stdout.String(), "version 0.1.2 is available") {
		t.Fatalf("stdout = %q, want the available version", stdout.String())
	}
	if env.transport.calls != 0 {
		t.Fatalf("the counting transport was used: %d requests", env.transport.calls)
	}
}

func TestUpdateUsageErrors(t *testing.T) {
	cases := [][]string{
		{"update", "extra"},
		{"update", "--bogus"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, stdout, stderr, stub := newDispatchApp("0.1.1")
			if got := a.run(context.Background(), args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", args, got, exitUsage)
			}
			if !strings.HasPrefix(stderr.String(), updateUsage) {
				t.Fatalf("stderr = %q, want the update usage first", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if stub.latestCalls != 0 {
				t.Fatalf("latest calls = %d, want none", stub.latestCalls)
			}
		})
	}
}

func TestUpdateHelpPrintsUsage(t *testing.T) {
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	if got := a.run(context.Background(), []string{"update", "--help"}); got != exitOK {
		t.Fatalf("run(update --help) = %d, want %d", got, exitOK)
	}
	if stdout.String() != updateUsage {
		t.Fatalf("stdout = %q, want %q", stdout.String(), updateUsage)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
