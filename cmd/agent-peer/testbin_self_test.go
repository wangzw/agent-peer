package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSandboxEnvFromEmpty checks that the child environment is built from
// nothing. If env started from os.Environ, the session variables set here
// would reach the Broker and make it fail with ErrInSession; if the runtime
// root were long, "<root>/<euid>/broker.sock" would exceed the Unix socket
// path limit. t.Setenv forbids t.Parallel, which is why this test is serial.
func TestSandboxEnvFromEmpty(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/x")
	t.Setenv("AGENT_PEER_SOCKET", "/y")

	sb := newSandbox(t)
	env := sb.env()

	got := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("environment entry %q has no =", kv)
		}
		if _, dup := got[name]; dup {
			t.Fatalf("duplicate environment variable %q", name)
		}
		got[name] = value
	}

	want := []string{"HOME", "PATH", "AGENT_PEER_TEST_HOME", "AGENT_PEER_TEST_RUNTIME_ROOT"}
	if len(got) != len(want) {
		t.Fatalf("env has %d variables, want exactly %d: %q", len(got), len(want), env)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("env is missing %s: %q", name, env)
		}
	}
	for _, name := range []string{"CLAUDE_CODE_MESSAGING_SOCKET", "AGENT_PEER_SOCKET"} {
		if _, ok := got[name]; ok {
			t.Fatalf("env leaked %s from the test process: %q", name, env)
		}
	}

	root := got["AGENT_PEER_TEST_RUNTIME_ROOT"]
	if !filepath.IsAbs(root) {
		t.Fatalf("runtime root %q is not absolute", root)
	}
	if len(root) > 60 {
		t.Fatalf("runtime root %q is %d bytes, want at most 60", root, len(root))
	}
	if root != sb.root {
		t.Fatalf("runtime root %q is not the sandbox root %q", root, sb.root)
	}
	if got["HOME"] != got["AGENT_PEER_TEST_HOME"] {
		t.Fatalf("HOME %q and AGENT_PEER_TEST_HOME %q differ", got["HOME"], got["AGENT_PEER_TEST_HOME"])
	}

	wantRuntime := filepath.Join(sb.root, strconv.Itoa(os.Geteuid()))
	if os.Geteuid() == 0 {
		wantRuntime = filepath.Join(sb.root, "root")
	}
	if dir := sb.runtimeDir(); dir != wantRuntime {
		t.Fatalf("runtimeDir() = %q, want %q", dir, wantRuntime)
	}
}

// TestOccupiedPortRefusesBind checks both helpers: the occupied port really
// refuses a second bind, and the free port really accepts one. Without this,
// a "listen unavailable" test could pass because the port was never taken.
func TestOccupiedPortRefusesBind(t *testing.T) {
	t.Parallel()

	port, held := occupiedPort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if l, err := net.Listen("tcp", addr); err == nil {
		l.Close()
		t.Fatalf("port %d was not occupied", port)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("close held listener: %v", err)
	}

	free := freePort(t)
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(free)))
	if err != nil {
		t.Fatalf("freePort returned %d, which cannot be listened on: %v", free, err)
	}
	l.Close()
}

// TestBinaryBuilt checks that TestMain produced a runnable binary. A swallowed
// build failure would otherwise surface as unrelated errors in every
// subprocess test.
func TestBinaryBuilt(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(testBinary)
	if err != nil {
		t.Fatalf("test binary: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("test binary %s is not executable (mode %v)", testBinary, info.Mode())
	}

	out, err := exec.Command(testBinary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
}
