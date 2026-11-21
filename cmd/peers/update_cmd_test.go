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
	"time"

	"github.com/wangzw/peers/internal/broker/update"
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
// binary and restores them when the test ends. The defaults describe an
// ordinary account: non-root, with a writable user-level installation.
type updateTestEnv struct {
	target    string
	transport *countingTransport
}

func newUpdateTestEnv(t *testing.T) *updateTestEnv {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "peers")
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
		restartBound time.Duration
		pollDelay    time.Duration
	}{
		updateExecutable, updateEvalSymlinks, updateEUID, updateNewClient,
		updateIsTerminal, updateGOOS, updateGOARCH, updateRestartWait, updateStatusPollDelay,
	}
	updateExecutable = func() (string, error) { return target, nil }
	updateEvalSymlinks = func(path string) (string, error) { return path, nil }
	updateEUID = func() int { return 1000 }
	updateNewClient = func() *http.Client { return &http.Client{Transport: env.transport} }
	updateIsTerminal = func(io.Reader) bool { return true }
	updateGOOS = "linux"
	updateGOARCH = "amd64"
	updateRestartWait = time.Second
	updateStatusPollDelay = time.Millisecond
	t.Cleanup(func() {
		updateExecutable = previous.executable
		updateEvalSymlinks = previous.evalSymlinks
		updateEUID = previous.euid
		updateNewClient = previous.newClient
		updateIsTerminal = previous.isTerminal
		updateGOOS = previous.goos
		updateGOARCH = previous.goarch
		updateRestartWait = previous.restartBound
		updateStatusPollDelay = previous.pollDelay
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

// userLevelRefusal is the answer a user-level Broker gives when it cannot
// apply an update itself.
var userLevelRefusal = errUserLevelUpdateUnsupported

// designSessionHint is the delegated-update hint as design-broker writes it.
// The tests spell it out so a change to the constant cannot pass unnoticed.
const designSessionHint = "Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically."

// designLocalHint is the local-replacement hint as design-broker writes it.
const designLocalHint = "The new binary takes effect the next time the Broker starts."

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
			if stub.latestCalls != 0 || len(stub.applyCalls) != 0 || stub.brokerApplyCalls != 0 {
				t.Fatalf("latest calls = %d, apply calls = %d, broker apply calls = %d, want none",
					stub.latestCalls, len(stub.applyCalls), stub.brokerApplyCalls)
			}
		})
	}
}

func TestUpdateRefusesNonTerminalInputWithoutYes(t *testing.T) {
	env := newUpdateTestEnv(t)
	updateIsTerminal = func(io.Reader) bool { return false }

	a, _, stderr, stub := newDispatchApp("0.1.1")
	a.stdin = strings.NewReader("y\n")
	if got := a.run(context.Background(), []string{"update"}); got != exitFailure {
		t.Fatalf("run(update) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("stderr = %q, want it to offer --yes", stderr.String())
	}
	if stub.brokerApplyCalls != 0 || stub.latestCalls != 0 || len(stub.applyCalls) != 0 {
		t.Fatalf("broker apply calls = %d, latest calls = %d, apply calls = %d, want none before confirmation",
			stub.brokerApplyCalls, stub.latestCalls, len(stub.applyCalls))
	}
	if env.transport.calls != 0 {
		t.Fatalf("network requests = %d, want none", env.transport.calls)
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateUserDeclines(t *testing.T) {
	env := newUpdateTestEnv(t)
	a, stdout, stderr, stub := newDispatchApp("0.1.1")
	a.stdin = strings.NewReader("n\n")

	if got := a.run(context.Background(), []string{"update"}); got != exitOK {
		t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Update canceled; nothing was changed.") {
		t.Fatalf("stdout = %q, want the cancellation line", stdout.String())
	}
	if stub.brokerApplyCalls != 0 || stub.latestCalls != 0 || len(stub.applyCalls) != 0 {
		t.Fatalf("broker apply calls = %d, latest calls = %d, apply calls = %d, want none",
			stub.brokerApplyCalls, stub.latestCalls, len(stub.applyCalls))
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateReportsUpToDate(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1"},
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "peers 0.1.1 is up to date\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if len(stub.applyCalls) != 0 {
		t.Fatalf("apply calls = %d, want none", len(stub.applyCalls))
	}
	assertBinaryUnchanged(t, env.target)
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
	target := filepath.Join(lockedDir, "peers")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })

	updateExecutable = func() (string, error) { return target, nil }
	updateEvalSymlinks = func(path string) (string, error) { return path, nil }

	stub := &stubDeps{brokerUpdateErr: userLevelRefusal}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if stub.brokerApplyCalls != 1 {
		t.Fatalf("broker apply calls = %d, want 1 before the fallback", stub.brokerApplyCalls)
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
	if !strings.Contains(stderr.String(), `run "sudo peers update"`) {
		t.Fatalf("stderr = %q, want the sudo hint", stderr.String())
	}
	assertBinaryUnchanged(t, target)
}

func TestUpdateFallsBackToLocalForAUserBroker(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdateErr: userLevelRefusal,
		latestRelease:   update.Release{Version: "0.1.2", URL: "https://example.test/release"},
		newerResult:     true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()
	a.stdin = strings.NewReader("yes\n")

	if got := a.run(context.Background(), []string{"update"}); got != exitOK {
		t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if stub.brokerApplyCalls != 1 {
		t.Fatalf("broker apply calls = %d, want 1 before the fallback", stub.brokerApplyCalls)
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
	out := stdout.String()
	if !strings.Contains(out, "[y/N]") {
		t.Fatalf("stdout = %q, want the confirmation question", out)
	}
	if !strings.Contains(out, designLocalHint+"\n") {
		t.Fatalf("stdout = %q, want the design local hint verbatim", out)
	}
	if strings.Contains(out, "Restarted") || strings.Contains(out, "service") {
		t.Fatalf("stdout = %q, want no service operation or message", out)
	}
}

func TestUpdateYesSkipsTheQuestion(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdateErr: userLevelRefusal,
		latestRelease:   update.Release{Version: "0.1.2"},
		newerResult:     true,
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

func TestUpdateRootUsesApplyUpdate(t *testing.T) {
	env := newUpdateTestEnv(t)
	updateEUID = func() int { return 0 }
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2"},
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if stub.brokerApplyCalls != 1 {
		t.Fatalf("broker apply calls = %d, want 1", stub.brokerApplyCalls)
	}
	if len(stub.applyCalls) != 0 {
		t.Fatalf("local apply calls = %d, want none for a system-level update", len(stub.applyCalls))
	}
	if !strings.Contains(stdout.String(), "peers 0.1.2 is installed.") {
		t.Fatalf("stdout = %q, want the installed version", stdout.String())
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateBrokerAppliesTheUpdate(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2"},
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if stub.brokerApplyCalls != 1 {
		t.Fatalf("broker apply calls = %d, want 1", stub.brokerApplyCalls)
	}
	if len(stub.applyCalls) != 0 || stub.latestCalls != 0 {
		t.Fatalf("local apply calls = %d, latest calls = %d, want none", len(stub.applyCalls), stub.latestCalls)
	}
	out := stdout.String()
	if !strings.Contains(out, "peers 0.1.2 is installed.") {
		t.Fatalf("stdout = %q, want the installed version", out)
	}
	if !strings.Contains(out, designSessionHint+"\n") {
		t.Fatalf("stdout = %q, want the design hint verbatim", out)
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateAsksBeforeTheBrokerCall(t *testing.T) {
	t.Run("declined", func(t *testing.T) {
		env := newUpdateTestEnv(t)
		a, stdout, stderr, stub := newDispatchApp("0.1.1")
		a.stdin = strings.NewReader("n\n")
		if got := a.run(context.Background(), []string{"update"}); got != exitOK {
			t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
		}
		if stub.brokerApplyCalls != 0 {
			t.Fatalf("broker apply calls = %d, want none after a refusal", stub.brokerApplyCalls)
		}
		if !strings.Contains(stdout.String(), "Update canceled") {
			t.Fatalf("stdout = %q, want the cancellation line", stdout.String())
		}
		assertBinaryUnchanged(t, env.target)
	})

	t.Run("accepted", func(t *testing.T) {
		newUpdateTestEnv(t)
		stub := &stubDeps{
			brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2"},
		}
		a, stdout, stderr, _ := newDispatchApp("0.1.1")
		a.deps = stub.deps()
		a.stdin = strings.NewReader("y\n")
		if got := a.run(context.Background(), []string{"update"}); got != exitOK {
			t.Fatalf("run(update) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
		}
		if stub.brokerApplyCalls != 1 {
			t.Fatalf("broker apply calls = %d, want 1", stub.brokerApplyCalls)
		}
		if !strings.Contains(stdout.String(), "[y/N]") {
			t.Fatalf("stdout = %q, want the question", stdout.String())
		}
	})
}

func TestUpdateWaitsForTheRestartedBroker(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2", Restarting: true},
	}
	var statusCalls int
	stub.brokerUpdateStatus = func(context.Context) (brokerUpdateStatus, error) {
		statusCalls++
		if statusCalls < 3 {
			return brokerUpdateStatus{CurrentVersion: "0.1.1"}, nil
		}
		return brokerUpdateStatus{CurrentVersion: "0.1.2"}, nil
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if statusCalls != 3 {
		t.Fatalf("UpdateStatus calls = %d, want 3 polls before the new version", statusCalls)
	}
	if !strings.Contains(stdout.String(), "The Broker is running peers 0.1.2.") {
		t.Fatalf("stdout = %q, want the running version after the restart", stdout.String())
	}
	if !strings.Contains(stdout.String(), designSessionHint+"\n") {
		t.Fatalf("stdout = %q, want the design hint verbatim", stdout.String())
	}
}

func TestUpdateRestartTimeoutNamesTheLog(t *testing.T) {
	newUpdateTestEnv(t)
	updateRestartWait = 0
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2", Restarting: true},
		brokerStatus: brokerUpdateStatus{CurrentVersion: "0.1.1"},
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	err := stderr.String()
	if !strings.Contains(err, "did not report version 0.1.2") {
		t.Fatalf("stderr = %q, want the timeout version", err)
	}
	if !strings.Contains(err, "journalctl -u peers.service") {
		t.Fatalf("stderr = %q, want the system service log location", err)
	}
}

func TestUpdateRestartTimeoutNamesTheUserLevelLog(t *testing.T) {
	newUpdateTestEnv(t)
	updateRestartWait = 0
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1", ToVersion: "0.1.2", Restarting: true, UserLevel: true},
		brokerStatus: brokerUpdateStatus{CurrentVersion: "0.1.1"},
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	err := stderr.String()
	if !strings.Contains(err, "check the service log with journalctl --user -u peers.service") {
		t.Fatalf("stderr = %q, want the user-level service log location", err)
	}
}

// TestUpdateLogHintPerPlatform pins the log location each platform's service
// manager writes to. A timeout on macOS must name the launchd log of the level
// that answered ApplyUpdate, as it does for systemd on Linux.
func TestUpdateLogHintPerPlatform(t *testing.T) {
	previous := updateGOOS
	t.Cleanup(func() { updateGOOS = previous })

	cases := []struct {
		goos      string
		userLevel bool
		want      string
	}{
		{"linux", false, "journalctl -u peers.service"},
		{"linux", true, "journalctl --user -u peers.service"},
		{"darwin", false, "/var/log/peers/broker.log (post-update helper: /var/log/peers/post-update.log)"},
		{"darwin", true, "~/Library/Logs/peers/broker.log (post-update helper: ~/Library/Logs/peers/post-update.log)"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/user="+fmt.Sprint(tc.userLevel), func(t *testing.T) {
			updateGOOS = tc.goos
			if got := updateLogHint(tc.userLevel); got != tc.want {
				t.Errorf("updateLogHint(%v) with GOOS %q = %q, want %q", tc.userLevel, tc.goos, got, tc.want)
			}
		})
	}
}

func TestUpdateBrokerPermissionDeniedMakesNoDownload(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdateErr: errors.New("permission denied: the account cannot write the Broker configuration"),
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Fatalf("stderr = %q, want the Broker's permission error", stderr.String())
	}
	if len(stub.applyCalls) != 0 {
		t.Fatalf("local apply calls = %d, want none after a permission failure", len(stub.applyCalls))
	}
	if stub.latestCalls != 0 {
		t.Fatalf("latest calls = %d, want none after a permission failure", stub.latestCalls)
	}
	if env.transport.calls != 0 {
		t.Fatalf("network requests = %d, want none", env.transport.calls)
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateBrokerUpToDate(t *testing.T) {
	newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdate: brokerUpdateResult{FromVersion: "0.1.1"},
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "peers 0.1.1 is up to date\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stub.brokerStatusCalls != 0 {
		t.Fatalf("UpdateStatus calls = %d, want none when nothing changed", stub.brokerStatusCalls)
	}
}

func TestUpdateWithoutARunningBrokerReplacesLocally(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdateErr: errNoRunningBroker,
		latestRelease:   update.Release{Version: "0.1.2"},
		newerResult:     true,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if stub.brokerApplyCalls != 1 {
		t.Fatalf("broker apply calls = %d, want 1 before the fallback", stub.brokerApplyCalls)
	}
	if len(stub.applyCalls) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(stub.applyCalls))
	}
	out := stdout.String()
	if !strings.Contains(out, designLocalHint+"\n") {
		t.Fatalf("stdout = %q, want the design local hint verbatim", out)
	}
	if strings.Contains(out, "Restarted") || strings.Contains(out, "service") {
		t.Fatalf("stdout = %q, want no service operation or message", out)
	}
	if stub.restartServiceCalls != 0 {
		t.Fatalf("service operations = %d, want none on the local replacement route", stub.restartServiceCalls)
	}
	assertBinaryUnchanged(t, env.target)
}

func TestUpdateApplyFailureLeavesTheBinary(t *testing.T) {
	env := newUpdateTestEnv(t)
	stub := &stubDeps{
		brokerUpdateErr: userLevelRefusal,
		latestRelease:   update.Release{Version: "0.1.2"},
		newerResult:     true,
		applyErr:        errors.New("checksum mismatch"),
	}
	a, _, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitFailure {
		t.Fatalf("run(update --yes) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("stderr = %q, want the apply error", stderr.String())
	}
	assertBinaryUnchanged(t, env.target)
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
		brokerUpdateErr: userLevelRefusal,
		updateAPIBase:   func() string { return srv.URL },
		updateLatest:    update.Latest,
		updateNewer:     update.Newer,
	}
	a, stdout, stderr, _ := newDispatchApp("0.1.1")
	a.deps = stub.deps()

	if got := a.run(context.Background(), []string{"update", "--yes"}); got != exitOK {
		t.Fatalf("run(update --yes) = %d, want %d (stderr %q)", got, exitOK, stderr.String())
	}
	if want := "/repos/wangzw/peers/releases/latest"; len(paths) != 1 || paths[0] != want {
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
