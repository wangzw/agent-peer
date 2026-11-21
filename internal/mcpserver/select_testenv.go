//go:build peers_testenv

package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// findNamedSocket honors PEERS_TEST_SOCKET, the test hook that replaced
// the removed AGENT_PEER_SOCKET. The variable is read only in builds with the
// peers_testenv tag. Its semantics are the old ones: the named socket is
// used, a socket that does not answer is an error rather than a fall-through to
// account selection, and the peer must be this account or root.
func (s *selector) findNamedSocket(ctx context.Context) (target, bool, error) {
	raw := s.environment("PEERS_TEST_SOCKET")
	if raw == "" {
		return target{}, false, nil
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return target{}, false, fmt.Errorf(
			"the socket %s named by PEERS_TEST_SOCKET is unavailable (%v)", raw, err)
	}
	r := s.probePath(ctx, path, kindTest)
	switch r.Outcome {
	case probeOK:
		return target{Path: path, Kind: kindTest}, true, nil
	case probeWrongPeer:
		return target{}, false, fmt.Errorf(
			"the socket %s named by PEERS_TEST_SOCKET is served by uid %d, not this account (%d) or root",
			path, r.PeerUID, os.Geteuid())
	default:
		return target{}, false, fmt.Errorf(
			"the socket %s named by PEERS_TEST_SOCKET is unavailable (%s)", path, probeDescription(r.Outcome))
	}
}

// environment returns the named environment variable through the selector's
// seam, falling back to the process environment.
func (s *selector) environment(name string) string {
	if s.getenv == nil {
		return os.Getenv(name)
	}
	return s.getenv(name)
}

// probeDescription names a probe failure for the test hook's error text.
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
