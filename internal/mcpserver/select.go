package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// targetKind names which step of the selection order chose the Broker.
type targetKind int

const (
	// kindEnv means AGENT_PEER_SOCKET named the socket.
	kindEnv targetKind = iota
	// kindUser means the user-level Broker of this account was chosen.
	kindUser
	// kindSystem means the system-level Broker was chosen.
	kindSystem
)

// target is a Broker socket together with the rule that selected it.
type target struct {
	Path string
	Kind targetKind
}

// systemRetryDeadline is how long step three keeps retrying a system-level
// socket that exists and may be connected to but has no process answering.
// It matches the readiness deadline of a launched Broker.
const systemRetryDeadline = 10 * time.Second

// systemRetryWaits are the waits between probes of a refused system-level
// socket. The remaining time up to systemRetryDeadline is waited last, so the
// total is exactly the deadline.
var systemRetryWaits = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

// selector applies the Broker selection order. The fields are seams for tests
// and for FindBroker, which supplies the production values.
type selector struct {
	getenv       func(string) string
	userSocket   string
	systemSocket string
	probe        func(ctx context.Context, path string) probeInfo
	launch       launcher // nil in Find
	after        func(time.Duration) <-chan time.Time
}

// Select returns the first target that holds, launching the user-level Broker
// when no socket answers. On failure the error text is shown to the model as
// the Detail of PhaseUnavailable or PhaseLaunchFailed; a *launchError means the
// Broker reported a launch failure.
func (s *selector) Select(ctx context.Context) (target, error) {
	t, found, err := s.find(ctx)
	if err != nil {
		return target{}, err
	}
	if found {
		return t, nil
	}
	if s.launch == nil {
		return target{}, errors.New("no running Broker found")
	}
	if err := s.launch.Launch(ctx); err != nil {
		return target{}, err
	}
	switch r := s.probePath(ctx, s.userSocket, kindUser); r.Outcome {
	case probeOK:
		return target{Path: s.userSocket, Kind: kindUser}, nil
	case probeWrongPeer:
		return target{}, peerMismatchError(s.userSocket, kindUser, r.PeerUID)
	default:
		return target{}, fmt.Errorf("the Broker reported ready but %s does not accept connections", s.userSocket)
	}
}

// Find applies steps one to three only. It never launches a Broker.
func (s *selector) Find(ctx context.Context) (target, error) {
	t, found, err := s.find(ctx)
	if err != nil {
		return target{}, err
	}
	if !found {
		return target{}, errors.New("no running Broker found")
	}
	return t, nil
}

// find runs steps one to three of the selection order.
func (s *selector) find(ctx context.Context) (target, bool, error) {
	if raw := s.environment("AGENT_PEER_SOCKET"); raw != "" {
		path, err := filepath.Abs(raw)
		if err != nil {
			return target{}, false, fmt.Errorf(
				"the socket %s named by AGENT_PEER_SOCKET is unavailable (%v)", raw, err)
		}
		r := s.probePath(ctx, path, kindEnv)
		switch r.Outcome {
		case probeOK:
			return target{Path: path, Kind: kindEnv}, true, nil
		case probeWrongPeer:
			return target{}, false, peerMismatchError(path, kindEnv, r.PeerUID)
		default:
			return target{}, false, fmt.Errorf(
				"the socket %s named by AGENT_PEER_SOCKET is unavailable (%s)", path, probeDescription(r.Outcome))
		}
	}

	switch r := s.probePath(ctx, s.userSocket, kindUser); r.Outcome {
	case probeOK:
		return target{Path: s.userSocket, Kind: kindUser}, true, nil
	case probeWrongPeer:
		// The socket at the user-level path is served by someone else. Falling
		// through would either use that process or launch a Broker that cannot
		// bind the path; report the socket as unusable instead.
		return target{}, false, peerMismatchError(s.userSocket, kindUser, r.PeerUID)
	}

	// For root the user-level and system-level sockets share one path, so the
	// probe above already covered the system-level Broker.
	if s.systemSocket != s.userSocket {
		ok, err := s.probeSystemWithRetry(ctx)
		if err != nil {
			return target{}, false, err
		}
		if ok {
			return target{Path: s.systemSocket, Kind: kindSystem}, true, nil
		}
	}
	return target{}, false, nil
}

// probeSystemWithRetry probes the system-level socket. A refused connection is
// retried for systemRetryDeadline, because a service manager may be restarting
// the Broker; a socket the account may not use, a missing socket and any other
// failure are not retried.
func (s *selector) probeSystemWithRetry(ctx context.Context) (bool, error) {
	switch s.probePath(ctx, s.systemSocket, kindSystem).Outcome {
	case probeOK:
		return true, nil
	case probeRefused:
	default:
		return false, nil
	}

	var waited time.Duration
	for _, d := range systemRetryWaits {
		if err := s.wait(ctx, d); err != nil {
			return false, err
		}
		waited += d
		if s.probePath(ctx, s.systemSocket, kindSystem).Outcome == probeOK {
			return true, nil
		}
	}
	if rest := systemRetryDeadline - waited; rest > 0 {
		if err := s.wait(ctx, rest); err != nil {
			return false, err
		}
		if s.probePath(ctx, s.systemSocket, kindSystem).Outcome == probeOK {
			return true, nil
		}
	}
	return false, nil
}

func (s *selector) wait(ctx context.Context, d time.Duration) error {
	select {
	case <-s.afterFunc()(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *selector) environment(name string) string {
	if s.getenv == nil {
		return os.Getenv(name)
	}
	return s.getenv(name)
}

// probePath probes path and applies the peer rule of the selection step. The
// raw probe already rejects a peer that may not serve a Broker at all; this
// narrows it to the account that may serve this particular socket.
func (s *selector) probePath(ctx context.Context, path string, kind targetKind) probeInfo {
	var info probeInfo
	if s.probe == nil {
		info = inspectSocket(ctx, path)
	} else {
		info = s.probe(ctx, path)
	}
	if info.Outcome == probeOK && info.PeerKnown && !peerAllowed(kind, info.PeerUID) {
		info.Outcome = probeWrongPeer
	}
	return info
}

// peerAllowed reports whether uid may serve the Broker socket of kind: root for
// the system-level Broker, this account for the user-level one, and either for
// the socket named by AGENT_PEER_SOCKET.
func peerAllowed(kind targetKind, uid uint32) bool {
	switch kind {
	case kindSystem:
		return uid == 0
	case kindEnv:
		return peerMayServe(uid)
	default:
		return uid == uint32(os.Geteuid())
	}
}

// peerMismatchError names the socket and the account that answered it. The text
// is shown to the model, so it must carry both facts.
func peerMismatchError(path string, kind targetKind, uid uint32) error {
	switch kind {
	case kindSystem:
		return fmt.Errorf("the system-level Broker socket %s is served by uid %d, not root", path, uid)
	case kindEnv:
		return fmt.Errorf("the socket %s named by AGENT_PEER_SOCKET is served by uid %d, not this account (%d) or root",
			path, uid, os.Geteuid())
	default:
		return fmt.Errorf("the user-level Broker socket %s is served by uid %d, not this account (%d)",
			path, uid, os.Geteuid())
	}
}

func (s *selector) afterFunc() func(time.Duration) <-chan time.Time {
	if s.after == nil {
		return time.After
	}
	return s.after
}

// probeDescription names a probe failure for the AGENT_PEER_SOCKET error text.
func probeDescription(r probeResult) string {
	switch r {
	case probeMissing:
		return "the path does not exist"
	case probeRefused:
		return "no process accepts connections"
	case probeDenied:
		return "this account may not connect to it"
	case probeOther:
		return "the connection failed"
	default:
		return "unknown failure"
	}
}

// FindBroker applies steps one to three of the Broker selection order and
// returns the socket path of the first that holds. It never launches a Broker.
// The error text is suitable for a terminal user.
func FindBroker(ctx context.Context) (string, error) {
	s := &selector{
		getenv:       os.Getenv,
		userSocket:   userSocketPath(os.Geteuid()),
		systemSocket: systemSocketPath(),
		probe:        inspectSocket,
		after:        time.After,
	}
	t, err := s.Find(ctx)
	if err != nil {
		return "", err
	}
	return t.Path, nil
}
