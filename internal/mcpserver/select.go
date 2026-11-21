package mcpserver

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// targetKind names which step of the selection order chose the Broker.
type targetKind int

const (
	// kindTest means the test build hook named the socket. A release build
	// never produces this kind.
	kindTest targetKind = iota
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

// SystemRetryDeadline is how long step two keeps retrying a system-level
// socket that exists and may be connected to but has no process answering. It
// is exported so end-to-end tests can derive their wait bounds from it rather
// than repeat the number.
const SystemRetryDeadline = 10 * time.Second

// systemRetryWaits are the waits between probes of a refused system-level
// socket. The remaining time up to SystemRetryDeadline is waited last, so the
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
	after        func(time.Duration) <-chan time.Time
}

// Find returns the first target of the selection order that holds. It never
// starts a Broker: when no socket answers it returns errNoBroker, whose text
// tells the model how to start one.
func (s *selector) Find(ctx context.Context) (target, error) {
	t, found, err := s.find(ctx)
	if err != nil {
		return target{}, err
	}
	if !found {
		return target{}, errNoBroker
	}
	return t, nil
}

// find runs the selection order. A test build may name a socket with its hook;
// a release build has no such step and always selects by account.
func (s *selector) find(ctx context.Context) (target, bool, error) {
	if t, found, err := s.findNamedSocket(ctx); err != nil || found {
		return t, found, err
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
// retried for SystemRetryDeadline, because a service manager may be restarting
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
	if rest := SystemRetryDeadline - waited; rest > 0 {
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
// the socket named by the test build hook.
func peerAllowed(kind targetKind, uid uint32) bool {
	switch kind {
	case kindSystem:
		return uid == 0
	case kindTest:
		return peerMayServe(uid)
	default:
		return uid == uint32(os.Geteuid())
	}
}

// peerMismatchError names a user-level or system-level socket and the account
// that answered it. The text is shown to the model, so it must carry both
// facts. A test-named socket builds its own message in the test build.
func peerMismatchError(path string, kind targetKind, uid uint32) error {
	switch kind {
	case kindSystem:
		return fmt.Errorf("the system-level Broker socket %s is served by uid %d, not root", path, uid)
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

// FindBroker applies the Broker selection order and returns the socket path of
// the first step that holds. It never starts a Broker. The error text is
// suitable for a terminal user.
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

// UserSocketPath returns the socket path of the calling account's user-level
// Broker. For root it is the same path as the system-level socket.
func UserSocketPath() string {
	return userSocketPath(os.Geteuid())
}

// FindBrokerWithDial is FindBroker together with a function that opens a
// connection to the selected socket. The function checks the peer of every
// underlying connection with peerUID before gRPC sends a byte, so a socket
// taken over by another account after selection is never used.
func FindBrokerWithDial(ctx context.Context) (string, func() (*grpc.ClientConn, localpb.LocalClient, error), error) {
	path, err := FindBroker(ctx)
	if err != nil {
		return "", nil, err
	}
	return path, func() (*grpc.ClientConn, localpb.LocalClient, error) {
		return dialLocal(path)
	}, nil
}
