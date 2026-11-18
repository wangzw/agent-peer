package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestMCPLaunchesBroker exercises the whole launch chain with the real binary:
// the mcp process finds no Broker, re-executes itself through the intermediate
// launcher, and the detached Broker then answers a separate status process.
// TestRealDepsAllWired only checks that the functions exist, and
// TestMCPHandshakeAndSignal points AGENT_PEER_SOCKET at a missing socket, so
// neither covers the launch.
func TestMCPLaunchesBroker(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	// The Broker must bind a port no other test claims; broker.listen defaults
	// to :17443 and a second bound Broker fails startup.
	sb.writeConfig(fmt.Sprintf("broker:\n  listen: %q\n", listenAddr(freePort(t))))
	// The process environment of a Claude Code session: the messaging socket is
	// set and AGENT_PEER_SOCKET is not, so the MCP server has to launch the
	// user-level Broker. The launcher drops the messaging socket before the
	// Broker starts; if it survived, the Broker would refuse to run in a session.
	sb.extraEnv = append(sb.extraEnv, "CLAUDE_CODE_MESSAGING_SOCKET=/s")

	// The Broker is started with Setsid, so it is not a child of the mcp
	// process and outlives it. Register the cleanup before startMCP: cleanups
	// run last in, first out, so the mcp process is stopped first and its
	// connection loop cannot launch another Broker while this one is terminated.
	t.Cleanup(func() { stopLaunchedBroker(t, sb) })

	p := startMCP(t, sb)
	p.initialize()

	// The connection loop runs in the background, so the first status may
	// still find no Broker. Poll until it answers or the 3s deadline passes.
	// Each attempt gets a fixed timeout and starts only while enough of the
	// deadline is left; shrinking the timeout along with the remaining time
	// would race runCLI's own limit against a status process that is exiting.
	const statusTimeout = time.Second
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Until(deadline) < statusTimeout/2 {
			t.Fatalf("agent-peer status did not exit 0 within 3s of the MCP initialize (stderr: %s)",
				p.stderr.String())
		}
		if res := runCLI(t, sb, statusTimeout, "status"); res.code == exitOK {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stopLaunchedBroker terminates the Broker that the MCP server launched. The
// launcher gives it its own session, so the pid has to come from the peer
// credentials of the account socket. The function must run after the mcp
// process has exited; otherwise the connection loop starts the Broker again.
func stopLaunchedBroker(t *testing.T, sb *sandbox) {
	t.Helper()

	sock := filepath.Join(sb.runtimeDir(), "broker.sock")
	pid, err := socketPeerPID(sock)
	if err != nil {
		// No Broker answered, so none was launched.
		return
	}

	_ = syscall.Kill(pid, syscall.SIGTERM)
	if waitLaunchedBrokerGone(pid, sock, 3*time.Second) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if !waitLaunchedBrokerGone(pid, sock, 3*time.Second) {
		t.Errorf("the launched Broker %d is still running after SIGKILL", pid)
	}
}

// waitLaunchedBrokerGone reports whether the Broker has exited. A process that
// no longer exists and a socket that no longer accepts connections both count:
// the Broker removes its socket while shutting down, and an orphaned process
// may linger as a zombie until the init process reaps it.
func waitLaunchedBrokerGone(pid int, sock string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if _, err := socketPeerPID(sock); err != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
