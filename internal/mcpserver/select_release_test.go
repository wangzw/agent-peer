//go:build !peers_testenv

package mcpserver

import (
	"context"
	"testing"

	"github.com/wangzw/peers/internal/testkit/fakelocal"
)

// TestReleaseBuildIgnoresSocketEnvironment proves that a release build reads
// neither the removed AGENT_PEER_SOCKET nor the test hook
// PEERS_TEST_SOCKET. The selector's environment seam records every
// lookup, and selection must complete without one: the account's user-level
// Broker wins even though both variables name another live socket.
func TestReleaseBuildIgnoresSocketEnvironment(t *testing.T) {
	envPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, envPath, fakelocal.Handlers{})
	userPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, userPath, fakelocal.Handlers{})
	systemPath := fakelocal.SocketPath(t)
	fakelocal.Start(t, systemPath, fakelocal.Handlers{})

	t.Setenv("AGENT_PEER_SOCKET", envPath)
	t.Setenv("PEERS_TEST_SOCKET", envPath)

	var asked []string
	s := &selector{
		getenv: func(name string) string {
			asked = append(asked, name)
			return ""
		},
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
	if len(asked) != 0 {
		t.Fatalf("the release selector looked up %q; it must read no socket environment variable", asked)
	}
}
