package mcpserver

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
)

// stubLauncher records how often it was asked to launch a Broker and lets the
// test decide what the launch does.
type stubLauncher struct {
	calls int
	fn    func(context.Context) error
}

func (l *stubLauncher) Launch(ctx context.Context) error {
	l.calls++
	if l.fn != nil {
		return l.fn(ctx)
	}
	return nil
}

// recordingAfter returns an after function that records each requested wait and
// releases it at once, so tests never sleep.
func recordingAfter() (func(time.Duration) <-chan time.Time, func() []time.Duration) {
	var waits []time.Duration
	after := func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		ch := make(chan time.Time)
		close(ch)
		return ch
	}
	return after, func() []time.Duration {
		return append([]time.Duration(nil), waits...)
	}
}

func wantWaits(t *testing.T, got []time.Duration, want ...time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("waits = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
}

// REQ-broker-config-026
func TestSelectEnvSocketTakesPrecedence(t *testing.T) {
	envPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, envPath, fakelocal.Handlers{})
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})
	systemPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, systemPath, fakelocal.Handlers{})

	s := &selector{
		getenv: func(name string) string {
			if name != "AGENT_PEER_SOCKET" {
				t.Errorf("getenv(%q), want AGENT_PEER_SOCKET", name)
			}
			return envPath
		},
		userSocket:   userPath,
		systemSocket: systemPath,
		probe:        inspectSocket,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindEnv || got.Path != envPath {
		t.Fatalf("Select = %+v, want {%s kindEnv}", got, envPath)
	}
}

// A dead AGENT_PEER_SOCKET must not silently fall through to another Broker.
func TestSelectEnvSocketMissingDoesNotFallThrough(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.sock")
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})
	launcher := &stubLauncher{}

	s := &selector{
		getenv:       func(string) string { return missing },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Select(context.Background())
	if err == nil {
		t.Fatal("Select returned nil error for a dead AGENT_PEER_SOCKET")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "AGENT_PEER_SOCKET") {
		t.Fatalf("error = %q, want it to name %s and AGENT_PEER_SOCKET", err, missing)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
}

// The user-level Broker wins over the system-level one (design:85).
func TestSelectUserBeforeSystem(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})
	systemPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, systemPath, fakelocal.Handlers{})

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe:        inspectSocket,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, userPath)
	}
}

func TestSelectSystemOnly(t *testing.T) {
	systemPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, systemPath, fakelocal.Handlers{})

	// The fake server runs as the test account, while a system-level Broker
	// must run as root. Pretend the peer is root so the rule is exercised on
	// the real probe.
	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return 0, nil }
	t.Cleanup(func() { peerUID = old })

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "broker.sock"),
		systemSocket: systemPath,
		probe:        inspectSocket,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindSystem || got.Path != systemPath {
		t.Fatalf("Select = %+v, want {%s kindSystem}", got, systemPath)
	}
}

// A system-level socket the account may not use is not retried; the session
// launches the user-level Broker instead (REQ-broker-config-027,
// REQ-broker-config-012).
func TestSelectSystemDeniedLaunches(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	started := false
	launcher := &stubLauncher{fn: func(context.Context) error {
		started = true
		return nil
	}}
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeDenied}
			}
			if started {
				return probeInfo{Outcome: probeOK}
			}
			return probeInfo{Outcome: probeMissing}
		},
		launch: launcher,
		after:  after,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, userPath)
	}
	if launcher.calls != 1 {
		t.Fatalf("launch called %d times, want 1", launcher.calls)
	}
	if w := waits(); len(w) != 0 {
		t.Fatalf("waits = %v, want none for a denied system socket", w)
	}
}

func TestSelectLaunchesUserBroker(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	launcher := &stubLauncher{fn: func(context.Context) error {
		fakelocal.Start(t, userPath, fakelocal.Handlers{})
		return nil
	}}

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, userPath)
	}
	if launcher.calls != 1 {
		t.Fatalf("launch called %d times, want 1", launcher.calls)
	}
}

func TestSelectReportsLaunchError(t *testing.T) {
	launchErr := &launchError{Reason: "listen address :17443 is unavailable"}
	launcher := &stubLauncher{fn: func(context.Context) error { return launchErr }}

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Select(context.Background())
	var got *launchError
	if !errors.As(err, &got) {
		t.Fatalf("Select error = %v, want *launchError", err)
	}
	if got != launchErr {
		t.Fatalf("Select error = %v, want the launcher's own error", got)
	}
}

func TestSelectLaunchReadyWithoutSocket(t *testing.T) {
	launcher := &stubLauncher{}

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Select(context.Background())
	if err == nil {
		t.Fatal("Select returned nil error after a launch that left no socket")
	}
	if !strings.Contains(err.Error(), "does not accept connections") {
		t.Fatalf("error = %q, want it to say does not accept connections", err)
	}
}

// A socket served by another account must not be selected: that process would
// receive the registration frame, message bodies, pairing keys and configuration
// writes. The user-level Broker of this account is not launched either, because
// the socket at its path belongs to someone else.
func TestSelectRejectsForeignUserSocket(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})

	foreign := uint32(os.Geteuid()) + 1
	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return foreign, nil }
	t.Cleanup(func() { peerUID = old })

	launcher := &stubLauncher{}
	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	got, err := s.Select(context.Background())
	if err == nil {
		t.Fatalf("Select = %+v, want an error for a socket served by uid %d", got, foreign)
	}
	if got.Kind == kindUser {
		t.Fatalf("Select = %+v, want not kindUser", got)
	}
	wantUID := strconv.FormatUint(uint64(foreign), 10)
	if !strings.Contains(err.Error(), userPath) || !strings.Contains(err.Error(), wantUID) {
		t.Fatalf("error = %q, want it to name %s and uid %s", err, userPath, wantUID)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
}

// AGENT_PEER_SOCKET names a socket that must be served by this account or by
// root. Another account falls under the same "unavailable" rule as a dead
// socket, and the error names the path and the account that answered.
func TestSelectRejectsForeignEnvSocket(t *testing.T) {
	envPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, envPath, fakelocal.Handlers{})

	foreign := uint32(os.Geteuid()) + 1
	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return foreign, nil }
	t.Cleanup(func() { peerUID = old })

	launcher := &stubLauncher{}
	s := &selector{
		getenv:       func(string) string { return envPath },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Select(context.Background())
	if err == nil {
		t.Fatalf("Select accepted a socket served by uid %d", foreign)
	}
	wantUID := strconv.FormatUint(uint64(foreign), 10)
	for _, want := range []string{envPath, wantUID, "AGENT_PEER_SOCKET"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
}

// Root may serve the system-level socket but not the user-level one of a
// regular account. The real peer check, not a stub probe, sees uid 0 on the
// account's socket.
func TestSelectRejectsRootServedUserSocket(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's own user-level socket is served by root")
	}
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})

	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return 0, nil }
	t.Cleanup(func() { peerUID = old })

	launcher := &stubLauncher{}
	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Select(context.Background())
	if err == nil {
		t.Fatal("Select accepted a user-level socket served by root")
	}
	if !strings.Contains(err.Error(), userPath) || !strings.Contains(err.Error(), "uid 0") {
		t.Fatalf("error = %q, want it to name %s and uid 0", err, userPath)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
}

// A system-level socket served by a non-root account is unusable, and selection
// goes on to launch the user-level Broker.
func TestSelectNonRootSystemSocketFallsThrough(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	launcher := &stubLauncher{fn: func(context.Context) error {
		fakelocal.Start(t, userPath, fakelocal.Handlers{})
		return nil
	}}

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(ctx context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeOK, PeerUID: 1, PeerKnown: true}
			}
			return inspectSocket(ctx, path)
		},
		launch: launcher,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, userPath)
	}
	if launcher.calls != 1 {
		t.Fatalf("launch called %d times, want 1", launcher.calls)
	}
}

// The terminal commands never launch a Broker (design-broker.md:84).
func TestFindNoBroker(t *testing.T) {
	launcher := &stubLauncher{}

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
		launch:       launcher,
	}

	_, err := s.Find(context.Background())
	if err == nil || err.Error() != "no running Broker found" {
		t.Fatalf("Find error = %v, want no running Broker found", err)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
}

// For root the user-level and system-level sockets are one path, so only one
// probe runs and the target is treated as user-level.
func TestSelectRootSharedSocketPath(t *testing.T) {
	same := filepath.Join(t.TempDir(), "broker.sock")
	probes := 0

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   same,
		systemSocket: same,
		probe: func(context.Context, string) probeInfo {
			probes++
			return probeInfo{Outcome: probeOK}
		},
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != same {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, same)
	}
	if probes != 1 {
		t.Fatalf("probed %d times, want 1", probes)
	}
}

// REQ-broker-config-026
func TestSelectSystemRefusedRetriesThenLaunches(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	started := false
	launcher := &stubLauncher{fn: func(context.Context) error {
		started = true
		return nil
	}}
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeRefused}
			}
			if started {
				return probeInfo{Outcome: probeOK}
			}
			return probeInfo{Outcome: probeMissing}
		},
		launch: launcher,
		after:  after,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Select = %+v, want {%s kindUser}", got, userPath)
	}
	if launcher.calls != 1 {
		t.Fatalf("launch called %d times, want 1", launcher.calls)
	}
	w := waits()
	wantWaits(t, w,
		250*time.Millisecond,
		500*time.Millisecond,
		time.Second,
		2*time.Second,
		4*time.Second,
		2250*time.Millisecond,
	)
	var total time.Duration
	for _, d := range w {
		total += d
	}
	if total != systemRetryDeadline {
		t.Fatalf("waits total %v, want %v", total, systemRetryDeadline)
	}
}

func TestSelectSystemRefusedThenUp(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	systemProbes := 0
	launcher := &stubLauncher{}
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path != systemPath {
				return probeInfo{Outcome: probeMissing}
			}
			systemProbes++
			if systemProbes <= 2 {
				return probeInfo{Outcome: probeRefused}
			}
			return probeInfo{Outcome: probeOK}
		},
		launch: launcher,
		after:  after,
	}

	got, err := s.Select(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Kind != kindSystem || got.Path != systemPath {
		t.Fatalf("Select = %+v, want {%s kindSystem}", got, systemPath)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
	wantWaits(t, waits(), 250*time.Millisecond, 500*time.Millisecond)
}

func TestFindSystemRefusedRetries(t *testing.T) {
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	launcher := &stubLauncher{}
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeRefused}
			}
			return probeInfo{Outcome: probeMissing}
		},
		launch: launcher,
		after:  after,
	}

	_, err := s.Find(context.Background())
	if err == nil || err.Error() != "no running Broker found" {
		t.Fatalf("Find error = %v, want no running Broker found", err)
	}
	if launcher.calls != 0 {
		t.Fatalf("launch called %d times, want 0", launcher.calls)
	}
	wantWaits(t, waits(),
		250*time.Millisecond,
		500*time.Millisecond,
		time.Second,
		2*time.Second,
		4*time.Second,
		2250*time.Millisecond,
	)
}

// FindBrokerWithDial returns a dialer that checks the peer of every connection
// it opens, so a socket taken over by another account after selection is
// refused before Hello is sent.
func TestFindBrokerWithDialRefusesForeignPeer(t *testing.T) {
	path := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, path, fakelocal.Handlers{})
	t.Setenv("AGENT_PEER_SOCKET", path)

	gotPath, dial, err := FindBrokerWithDial(context.Background())
	if err != nil {
		t.Fatalf("FindBrokerWithDial: %v", err)
	}
	if gotPath != path {
		t.Fatalf("FindBrokerWithDial path = %q, want %q", gotPath, path)
	}

	// The socket now answers as another account, as if another process had
	// taken it over between selection and the connection.
	foreign := uint32(os.Geteuid()) + 1
	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return foreign, nil }
	t.Cleanup(func() { peerUID = old })

	cc, client, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = cc.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	_, err = client.Hello(ctx, &localpb.HelloRequest{ProtocolVersion: localpb.ProtocolVersion})
	if err == nil {
		t.Fatal("the dialer connected to a Broker socket served by another account")
	}
	if !strings.Contains(err.Error(), "served by uid") {
		t.Fatalf("Hello error = %q, want it to name the peer uid", err)
	}
	for _, c := range srv.Calls() {
		if c.Method == "Hello" {
			t.Fatal("the fake Broker received Hello over a connection served by another account")
		}
	}
}
