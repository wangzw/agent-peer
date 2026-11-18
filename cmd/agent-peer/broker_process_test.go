package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dialBroker fails the test when the address is not accepting connections.
func dialBroker(t *testing.T, addr string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	_ = conn.Close()
}

// assertDialFails fails the test when something accepts connections on addr.
func assertDialFails(t *testing.T, addr string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("dial %s succeeded, want the address to stay unbound", addr)
	}
}

// waitListen polls until the Broker accepts connections on addr. A process
// started without a readiness pipe has no other way to report readiness; this
// polls an external condition rather than replacing a decision with a sleep.
// It reports false when the process exits or the deadline passes first.
func waitListen(t *testing.T, p *brokerProc, addr string) bool {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-p.exited:
			return false
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		// Cap the dial and the retry interval by the time left, so one last
		// attempt cannot push the whole wait past the deadline.
		dialTimeout := 200 * time.Millisecond
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
		// Recompute the remaining time after the dial, which may have consumed
		// all of it; the next iteration reports the timeout.
		poll := time.Until(deadline)
		if poll > 20*time.Millisecond {
			poll = 20 * time.Millisecond
		}
		<-time.After(poll)
	}
}

// startBrokerTerminal runs "agent-peer broker" the way a terminal launch does:
// no readiness pipe and no --ready-fd. The returned process has no reader, so
// readiness is observed by dialing the listen address.
func startBrokerTerminal(t *testing.T, sb *sandbox, args ...string) *brokerProc {
	t.Helper()

	cmd := exec.Command(testBinary, append([]string{"broker"}, args...)...)
	cmd.Env = sb.env()
	cmd.WaitDelay = waitDelay
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	p := &brokerProc{t: t, cmd: cmd, stderr: stderr, exited: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(p.stop)
	return p
}

// startBrokerTerminalOnFreePort starts a terminal Broker with --listen on a
// port that freePort released and waits until it accepts connections. The port
// can be taken between freePort's close and the Broker's bind, so a Broker that
// exits because the address is unavailable is retried once on a fresh port.
func startBrokerTerminalOnFreePort(t *testing.T, sb *sandbox, args ...string) (*brokerProc, int) {
	t.Helper()

	const attempts = 2
	for attempt := 1; ; attempt++ {
		port := freePort(t)
		addr := listenAddr(port)
		p := startBrokerTerminal(t, sb, append([]string{"--listen", addr}, args...)...)
		if waitListen(t, p, addr) {
			return p, port
		}
		select {
		case <-p.exited:
		default:
			t.Fatalf("broker did not listen on %s within 3s while still running (stderr: %s)", addr, p.stderr.String())
		}
		code := p.cmd.ProcessState.ExitCode()
		if code != exitListenUnavailable || attempt == attempts {
			t.Fatalf("broker start on %s: exit code %d (stderr: %s)", addr, code, p.stderr.String())
		}
		// Another process took the port between freePort's close and the
		// Broker's bind. The Broker has exited; retry on a fresh port.
		p.stop()
	}
}

// TestBrokerListenFlagOverridesConfig covers REQ-broker-config-025 and
// REQ-broker-config-029: --listen replaces the configured address for this
// run, and the configuration file is left untouched.
func TestBrokerListenFlagOverridesConfig(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	configPort, _ := occupiedPort(t)
	sb.writeConfig(fmt.Sprintf("broker:\n  listen: %q\n", listenAddr(configPort)))
	before, err := os.ReadFile(sb.configPath())
	if err != nil {
		t.Fatalf("read config before start: %v", err)
	}

	p, listenPort := startBrokerOnFreePort(t, sb)
	dialBroker(t, listenAddr(listenPort))

	after, err := os.ReadFile(sb.configPath())
	if err != nil {
		t.Fatalf("read config after start: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config file changed:\nbefore %q\nafter  %q", before, after)
	}

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal broker: %v", err)
	}
	if code := p.wait(3 * time.Second); code != exitOK {
		t.Fatalf("exit code after SIGTERM = %d, want %d (stderr: %s)", code, exitOK, p.stderr.String())
	}

	// The Broker also owns the configuration file during shutdown; compare once
	// more after it has exited, not only after it became ready.
	afterExit, err := os.ReadFile(sb.configPath())
	if err != nil {
		t.Fatalf("read config after exit: %v", err)
	}
	if !bytes.Equal(before, afterExit) {
		t.Fatalf("config file changed after exit:\nbefore %q\nafter  %q", before, afterExit)
	}
}

// TestBrokerSecondInstanceFailsWithAnotherListenAddress covers
// REQ-broker-config-013: a second instance fails whatever address it asks for,
// and the first one keeps serving.
func TestBrokerSecondInstanceFailsWithAnotherListenAddress(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	first, firstPort := startBrokerOnFreePort(t, sb)

	// Pick the second address while the first Broker holds its own port, so
	// the two addresses differ. The second instance acquires the account lock
	// before it binds, so a port taken by another process cannot change its
	// exit code and this start needs no retry.
	secondPort := freePort(t)

	second := startBroker(t, sb, "--listen", listenAddr(secondPort))
	ready := second.readyLine(3 * time.Second)
	if code := second.wait(3 * time.Second); code != exitAlreadyRunning {
		t.Fatalf("second exit code = %d, want %d (stderr: %s)", code, exitAlreadyRunning, second.stderr.String())
	}
	if want := "agent-peer broker: " + ready; second.lastStderrLine("agent-peer broker: ") != want {
		t.Fatalf("second stderr line = %q, want %q", second.lastStderrLine("agent-peer broker: "), want)
	}
	if !strings.Contains(ready, "already running") {
		t.Fatalf("second ready line = %q, want it to contain %q", ready, "already running")
	}
	assertDialFails(t, listenAddr(secondPort))

	if err := first.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("first broker is not running after the second failed: %v", err)
	}
	dialBroker(t, listenAddr(firstPort))

	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal first broker: %v", err)
	}
	if code := first.wait(3 * time.Second); code != exitOK {
		t.Fatalf("first exit code after SIGTERM = %d, want %d (stderr: %s)", code, exitOK, first.stderr.String())
	}

	// The lock is free again, so a Broker can start. The helper retries on a
	// fresh port if this one was taken after freePort released it.
	startBrokerOnFreePort(t, sb)
}

// TestBrokerListenAddressUnavailable covers REQ-broker-config-024: an
// unavailable address fails the start instead of falling back to another one.
func TestBrokerListenAddressUnavailable(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	port, _ := occupiedPort(t)
	addr := listenAddr(port)

	p := startBroker(t, sb, "--listen", addr)
	ready := p.readyLine(3 * time.Second)
	if code := p.wait(3 * time.Second); code != exitListenUnavailable {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitListenUnavailable, p.stderr.String())
	}
	if !strings.Contains(ready, addr) {
		t.Fatalf("ready line = %q, want it to contain %q", ready, addr)
	}
	if want := "agent-peer broker: " + ready; p.lastStderrLine("agent-peer broker: ") != want {
		t.Fatalf("stderr line = %q, want %q", p.lastStderrLine("agent-peer broker: "), want)
	}
}

func TestBrokerInvalidConfig(t *testing.T) {
	t.Parallel()

	t.Run("malformed YAML", func(t *testing.T) {
		t.Parallel()

		sb := newSandbox(t)
		sb.writeConfig("broker: [\n")

		p := startBroker(t, sb)
		ready := p.readyLine(3 * time.Second)
		if code := p.wait(3 * time.Second); code != exitInvalidConfig {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitInvalidConfig, p.stderr.String())
		}
		if !strings.Contains(ready, "config file parse error") {
			t.Fatalf("ready line = %q, want it to contain %q", ready, "config file parse error")
		}
		if want := "agent-peer broker: " + ready; p.lastStderrLine("agent-peer broker: ") != want {
			t.Fatalf("stderr line = %q, want %q", p.lastStderrLine("agent-peer broker: "), want)
		}
	})

	t.Run("unreadable config", func(t *testing.T) {
		t.Parallel()

		sb := newSandbox(t)
		// A directory in place of the file makes the read fail with its path.
		if err := os.MkdirAll(sb.configPath(), 0o700); err != nil {
			t.Fatalf("create config path as a directory: %v", err)
		}

		p := startBroker(t, sb)
		ready := p.readyLine(3 * time.Second)
		if code := p.wait(3 * time.Second); code != exitInvalidConfig {
			t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitInvalidConfig, p.stderr.String())
		}
		if !strings.Contains(ready, sb.configPath()) {
			t.Fatalf("ready line = %q, want it to name the config file %q", ready, sb.configPath())
		}
	})
}

func TestBrokerInsideSession(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	sb.extraEnv = append(sb.extraEnv, "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/x.sock")

	p := startBroker(t, sb)
	ready := p.readyLine(3 * time.Second)
	if code := p.wait(3 * time.Second); code != exitInSession {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitInSession, p.stderr.String())
	}
	if ready == "" {
		t.Fatal("ready line is empty, want the session failure reason")
	}
	if want := "agent-peer broker: " + ready; p.lastStderrLine("agent-peer broker: ") != want {
		t.Fatalf("stderr line = %q, want %q", p.lastStderrLine("agent-peer broker: "), want)
	}
}

func TestBrokerUsesTestHome(t *testing.T) {
	t.Parallel()

	account, err := user.Current()
	if err != nil {
		t.Fatalf("find the developer account: %v", err)
	}
	realData := filepath.Join(account.HomeDir, ".agent-peer")
	before, beforeErr := os.Stat(realData)

	first, second := newSandbox(t), newSandbox(t)
	first.writeConfig("broker:\n  alias: host-a\n")
	second.writeConfig("broker:\n  alias: host-b\n")

	startBrokerOnFreePort(t, first)
	startBrokerOnFreePort(t, second)

	for _, sb := range []*sandbox{first, second} {
		for _, name := range []string{"identity.key", "identity.crt"} {
			path := filepath.Join(sb.home, ".agent-peer", name)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("identity file %s: %v", path, err)
			}
		}
	}

	after, afterErr := os.Stat(realData)
	switch {
	case beforeErr == nil:
		if afterErr != nil {
			t.Fatalf("real %s disappeared during the test: %v", realData, afterErr)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("real %s was modified: mtime %v -> %v", realData, before.ModTime(), after.ModTime())
		}
	case os.IsNotExist(beforeErr):
		if !os.IsNotExist(afterErr) {
			t.Fatalf("real %s appeared during the test (%v)", realData, afterErr)
		}
	default:
		t.Fatalf("stat real %s before the test: %v", realData, beforeErr)
	}
}

func TestBrokerRuntimeDirBadPermissions(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	dir := sb.runtimeDir()
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	// Chmod after Mkdir so the umask cannot narrow the mode the check rejects.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod runtime directory: %v", err)
	}

	p := startBroker(t, sb)
	ready := p.readyLine(3 * time.Second)
	if code := p.wait(3 * time.Second); code != exitRuntimeDir {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitRuntimeDir, p.stderr.String())
	}
	if !strings.Contains(ready, dir) {
		t.Fatalf("ready line = %q, want it to name the runtime directory %q", ready, dir)
	}
}

func TestBrokerInvalidListenValue(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	p := startBroker(t, sb, "--listen", "not-an-address")
	ready := p.readyLine(3 * time.Second)
	if code := p.wait(3 * time.Second); code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, p.stderr.String())
	}
	if stderr := p.stderr.String(); !strings.Contains(stderr, "--listen") {
		t.Fatalf("stderr = %q, want it to mention %q", stderr, "--listen")
	}
	if stderr := p.stderr.String(); !strings.Contains(stderr, "usage: agent-peer broker") {
		t.Fatalf("stderr = %q, want the broker usage", stderr)
	}
	if !strings.Contains(ready, "--listen") {
		t.Fatalf("ready line = %q, want it to mention %q", ready, "--listen")
	}
}

// TestBrokerSystemRequiresRoot checks that --system without root ends with the
// dedicated exit code and names the reason. As root the command is supposed to
// proceed and serve the system Broker, so the case is skipped instead.
func TestBrokerSystemRequiresRoot(t *testing.T) {
	t.Parallel()

	if euid := os.Geteuid(); euid == 0 {
		t.Skip("the test process runs as root: --system is expected to proceed and serve the system Broker")
	}

	sb := newSandbox(t)
	p := startBroker(t, sb, "--system")
	ready := p.readyLine(3 * time.Second)
	if code := p.wait(3 * time.Second); code != exitNotRoot {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitNotRoot, p.stderr.String())
	}
	if stderr := p.stderr.String(); !strings.Contains(stderr, "must run as root") {
		t.Fatalf("stderr = %q, want it to name the root requirement", stderr)
	}
	if !strings.Contains(ready, "must run as root") {
		t.Fatalf("ready line = %q, want it to name the root requirement", ready)
	}
	if want := "agent-peer broker: " + ready; p.lastStderrLine("agent-peer broker: ") != want {
		t.Fatalf("stderr line = %q, want %q", p.lastStderrLine("agent-peer broker: "), want)
	}
}

func TestBrokerFromTerminalHasNoReadyPipe(t *testing.T) {
	t.Parallel()

	sb := newSandbox(t)
	p, _ := startBrokerTerminalOnFreePort(t, sb)

	// The helper observed readiness by dialing the listen address; the
	// process has no readiness pipe to read.

	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal broker: %v", err)
	}
	if code := p.wait(3 * time.Second); code != exitOK {
		t.Fatalf("exit code after SIGINT = %d, want %d (stderr: %s)", code, exitOK, p.stderr.String())
	}
}
