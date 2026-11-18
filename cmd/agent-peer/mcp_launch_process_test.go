package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// socketPeerPIDSupported reports whether this platform can find the process
// serving a Unix socket. socketpeer_other_test.go clears it where it cannot.
var socketPeerPIDSupported = true

// TestMCPLaunchesBroker exercises the whole launch chain with the real binary:
// the mcp process finds no Broker, re-executes itself through the intermediate
// launcher, and the detached Broker then answers a separate status process.
// TestRealDepsAllWired only checks that the functions exist, and
// TestMCPHandshakeAndSignal points AGENT_PEER_SOCKET at a missing socket, so
// neither covers the launch.
func TestMCPLaunchesBroker(t *testing.T) {
	if !socketPeerPIDSupported {
		t.Skip("the launched Broker cannot be found by pid on this platform, and skipping keeps it from being leaked")
	}
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
	// Each iteration recomputes the remaining time and uses it as the limit of
	// the next status command, so the whole loop stays within 3s.
	deadline := time.Now().Add(3 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("agent-peer status did not exit 0 within 3s of the MCP initialize (stderr: %s)",
				p.stderr.String())
		}
		if res := runCLI(t, sb, remaining, "status"); res.code == exitOK {
			return
		}
		if rest := time.Until(deadline); rest > 0 {
			pause := 20 * time.Millisecond
			if rest < pause {
				pause = rest
			}
			time.Sleep(pause)
		}
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
	if waitPIDGone(pid, 3*time.Second) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if !waitPIDGone(pid, 3*time.Second) {
		t.Errorf("the launched Broker %d is still running after SIGKILL", pid)
	}
}

// waitPIDGone reports whether pid has exited. kill(pid, 0) returning ESRCH is
// the only sign used: a socket that no longer accepts connections says nothing
// about the process, which may still be running without its listener.
func waitPIDGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
