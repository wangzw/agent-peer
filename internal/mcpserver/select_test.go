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

	"github.com/wangzw/peers/internal/testkit/fakelocal"
)

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

// REQ-broker-config-026: the removed AGENT_PEER_SOCKET no longer influences
// selection; the user-level Broker of the account wins.
func TestSelectIgnoresSocketEnvironment(t *testing.T) {
	envPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, envPath, fakelocal.Handlers{})
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})
	systemPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, systemPath, fakelocal.Handlers{})
	t.Setenv("AGENT_PEER_SOCKET", envPath)

	s := &selector{
		userSocket:   userPath,
		systemSocket: systemPath,
		probe:        inspectSocket,
	}

	got, err := s.Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Find = %+v, want {%s kindUser} despite AGENT_PEER_SOCKET naming %s", got, userPath, envPath)
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

	got, err := s.Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Kind != kindUser || got.Path != userPath {
		t.Fatalf("Find = %+v, want {%s kindUser}", got, userPath)
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

	got, err := s.Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Kind != kindSystem || got.Path != systemPath {
		t.Fatalf("Find = %+v, want {%s kindSystem}", got, systemPath)
	}
}

// A system-level socket the account may not use is not retried, and with no
// user-level Broker there is nothing to select (REQ-broker-config-027,
// REQ-broker-config-038).
func TestFindSystemDeniedReturnsNoBroker(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeDenied}
			}
			return probeInfo{Outcome: probeMissing}
		},
		after: after,
	}

	_, err := s.Find(context.Background())
	if !errors.Is(err, errNoBroker) {
		t.Fatalf("Find error = %v, want errNoBroker", err)
	}
	if w := waits(); len(w) != 0 {
		t.Fatalf("waits = %v, want none for a denied system socket", w)
	}
}

// A socket served by another account must not be selected: that process would
// receive the registration frame, message bodies, pairing keys and configuration
// writes.
func TestSelectRejectsForeignUserSocket(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})

	foreign := uint32(os.Geteuid()) + 1
	old := peerUID
	peerUID = func(net.Conn) (uint32, error) { return foreign, nil }
	t.Cleanup(func() { peerUID = old })

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
	}

	got, err := s.Find(context.Background())
	if err == nil {
		t.Fatalf("Find = %+v, want an error for a socket served by uid %d", got, foreign)
	}
	if got.Kind == kindUser {
		t.Fatalf("Find = %+v, want not kindUser", got)
	}
	wantUID := strconv.FormatUint(uint64(foreign), 10)
	if !strings.Contains(err.Error(), userPath) || !strings.Contains(err.Error(), wantUID) {
		t.Fatalf("error = %q, want it to name %s and uid %s", err, userPath, wantUID)
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

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
	}

	_, err := s.Find(context.Background())
	if err == nil {
		t.Fatal("Find accepted a user-level socket served by root")
	}
	if !strings.Contains(err.Error(), userPath) || !strings.Contains(err.Error(), "uid 0") {
		t.Fatalf("error = %q, want it to name %s and uid 0", err, userPath)
	}
}

// A system-level socket served by a non-root account is unusable, and with no
// user-level Broker there is nothing to select.
func TestFindNonRootSystemSocketFallsThrough(t *testing.T) {
	userPath := fakelocal.SocketPath(t)
	systemPath := filepath.Join(t.TempDir(), "system.sock")

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
	}

	_, err := s.Find(context.Background())
	if !errors.Is(err, errNoBroker) {
		t.Fatalf("Find error = %v, want errNoBroker", err)
	}
}

// No Broker at all is an error whose text names both ways to start one
// (REQ-broker-config-038).
func TestFindNoBroker(t *testing.T) {
	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   filepath.Join(t.TempDir(), "user.sock"),
		systemSocket: filepath.Join(t.TempDir(), "system.sock"),
		probe:        inspectSocket,
	}

	_, err := s.Find(context.Background())
	if !errors.Is(err, errNoBroker) {
		t.Fatalf("Find error = %v, want errNoBroker", err)
	}
	for _, want := range []string{"ask the administrator", "peers service install", "peers service run", "code 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
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

	got, err := s.Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Kind != kindUser || got.Path != same {
		t.Fatalf("Find = %+v, want {%s kindUser}", got, same)
	}
	if probes != 1 {
		t.Fatalf("probed %d times, want 1", probes)
	}
}

// REQ-broker-config-026
func TestFindSystemRefusedRetriesThenNoBroker(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	after, waits := recordingAfter()

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeRefused}
			}
			return probeInfo{Outcome: probeMissing}
		},
		after: after,
	}

	_, err := s.Find(context.Background())
	if !errors.Is(err, errNoBroker) {
		t.Fatalf("Find error = %v, want errNoBroker", err)
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
	if total != SystemRetryDeadline {
		t.Fatalf("waits total %v, want %v", total, SystemRetryDeadline)
	}
}

func TestSelectSystemRefusedThenUp(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")
	systemProbes := 0
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
		after: after,
	}

	got, err := s.Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Kind != kindSystem || got.Path != systemPath {
		t.Fatalf("Find = %+v, want {%s kindSystem}", got, systemPath)
	}
	wantWaits(t, waits(), 250*time.Millisecond, 500*time.Millisecond)
}

func TestFindSystemRefusedRetries(t *testing.T) {
	systemPath := filepath.Join(t.TempDir(), "system.sock")
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
		after: after,
	}

	_, err := s.Find(context.Background())
	if !errors.Is(err, errNoBroker) {
		t.Fatalf("Find error = %v, want errNoBroker", err)
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

// REQ-broker-config-055: a command that names a level talks to that level's
// Broker, so the two explicit levels pick different sockets even when both
// Brokers answer.
func TestFindAtLevelSelectsOnlyThatLevel(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			return probeInfo{Outcome: probeOK}
		},
	}

	userTarget, found, err := s.findAtLevel(context.Background(), false)
	if err != nil || !found {
		t.Fatalf("findAtLevel(user) = (%+v, %v, %v), want the user-level socket", userTarget, found, err)
	}
	if userTarget.Kind != kindUser || userTarget.Path != userPath {
		t.Fatalf("findAtLevel(user) = %+v, want {%s kindUser}", userTarget, userPath)
	}

	systemTarget, found, err := s.findAtLevel(context.Background(), true)
	if err != nil || !found {
		t.Fatalf("findAtLevel(system) = (%+v, %v, %v), want the system-level socket", systemTarget, found, err)
	}
	if systemTarget.Kind != kindSystem || systemTarget.Path != systemPath {
		t.Fatalf("findAtLevel(system) = %+v, want {%s kindSystem}", systemTarget, systemPath)
	}
}

// REQ-broker-config-055: --user must not fall through to a running system-level
// Broker; otherwise the option would not decide which Broker the command talks
// to.
func TestFindAtLevelUserDoesNotFallThrough(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeOK}
			}
			return probeInfo{Outcome: probeMissing}
		},
	}

	got, found, err := s.findAtLevel(context.Background(), false)
	if err != nil {
		t.Fatalf("findAtLevel(user): %v", err)
	}
	if found {
		t.Fatalf("findAtLevel(user) = %+v, want no target despite the running system-level Broker", got)
	}
}

// TestFindForLevelExplainsAnEmptySelection checks the error text of an empty
// selection. A named --system level reports the system-level Broker as
// unavailable; a defaulted level points at --system when only the system-level
// Broker answers; a named --user level keeps the generic text.
func TestFindForLevelExplainsAnEmptySelection(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")

	cases := []struct {
		name          string
		system        bool
		explicit      bool
		root          bool
		userOutcome   probeResult
		systemOutcome probeResult
		want          error
	}{
		{
			name: "named system level", system: true, explicit: true,
			userOutcome: probeMissing, systemOutcome: probeMissing,
			want: errNoSystemBroker,
		},
		{
			// Root's default level is the system level, and root is the
			// administrator, so an empty selection must report the
			// system-level Broker rather than the account's user-level one.
			name: "root default level", system: true, explicit: false, root: true,
			userOutcome: probeMissing, systemOutcome: probeMissing,
			want: errNoSystemBroker,
		},
		{
			name: "default level with the system Broker running", system: false, explicit: false,
			userOutcome: probeMissing, systemOutcome: probeOK,
			want: errNoUserBrokerSystemRunning,
		},
		{
			name: "named user level", system: false, explicit: true,
			userOutcome: probeMissing, systemOutcome: probeOK,
			want: errNoBroker,
		},
		{
			name: "default level with nothing running", system: false, explicit: false,
			userOutcome: probeMissing, systemOutcome: probeMissing,
			want: errNoBroker,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userSocket := userPath
			if tc.root {
				// Root's user-level and system-level sockets share one path.
				userSocket = systemPath
			}
			s := &selector{
				userSocket:   userSocket,
				systemSocket: systemPath,
				probe: func(_ context.Context, path string) probeInfo {
					if path == userSocket {
						return probeInfo{Outcome: tc.userOutcome}
					}
					return probeInfo{Outcome: tc.systemOutcome}
				},
			}
			_, err := s.findForLevel(context.Background(), tc.system, tc.explicit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("findForLevel error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestFindForLevelReturnsTheSelectedSocket checks the successful path of the
// level-aware finder.
func TestFindForLevelReturnsTheSelectedSocket(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")

	s := &selector{
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == systemPath {
				return probeInfo{Outcome: probeOK}
			}
			return probeInfo{Outcome: probeMissing}
		},
	}
	got, err := s.findForLevel(context.Background(), true, true)
	if err != nil {
		t.Fatalf("findForLevel: %v", err)
	}
	if got != systemPath {
		t.Errorf("findForLevel = %q, want %q", got, systemPath)
	}
}

// REQ-broker-config-055: --system must not fall through to a running user-level
// Broker either.
func TestFindAtLevelSystemDoesNotFallThrough(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user.sock")
	systemPath := filepath.Join(t.TempDir(), "system.sock")

	s := &selector{
		getenv:       func(string) string { return "" },
		userSocket:   userPath,
		systemSocket: systemPath,
		probe: func(_ context.Context, path string) probeInfo {
			if path == userPath {
				return probeInfo{Outcome: probeOK}
			}
			return probeInfo{Outcome: probeMissing}
		},
	}

	got, found, err := s.findAtLevel(context.Background(), true)
	if err != nil {
		t.Fatalf("findAtLevel(system): %v", err)
	}
	if found {
		t.Fatalf("findAtLevel(system) = %+v, want no target despite the running user-level Broker", got)
	}
}
