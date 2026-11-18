package harness

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// envMap turns NAME=value strings into a map.
func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, variable := range env {
		name, value, ok := strings.Cut(variable, "=")
		if ok {
			values[name] = value
		}
	}
	return values
}

func TestLocalHostEnvIsolates(t *testing.T) {
	first := NewLocalHost(t, LocalOptions{Name: "first"})
	second := NewLocalHost(t, LocalOptions{Name: "second"})

	firstEnv := envMap(first.impl.(*localNode).env.Build(nil))
	secondEnv := envMap(second.impl.(*localNode).env.Build(nil))

	for name, env := range map[string]map[string]string{
		"first":  firstEnv,
		"second": secondEnv,
	} {
		root, ok := env["AGENT_PEER_TEST_RUNTIME_ROOT"]
		if !ok {
			t.Fatalf("%s node has no AGENT_PEER_TEST_RUNTIME_ROOT in %v", name, env)
		}
		acct, ok := env["AGENT_PEER_TEST_HOME"]
		if !ok {
			t.Fatalf("%s node has no AGENT_PEER_TEST_HOME in %v", name, env)
		}
		if !filepath.IsAbs(root) {
			t.Errorf("%s node runtime root %q is not absolute", name, root)
		}
		if !filepath.IsAbs(acct) {
			t.Errorf("%s node account home %q is not absolute", name, acct)
		}
		if root == acct {
			t.Errorf("%s node uses %q as both runtime root and account home", name, root)
		}
		if !filepath.IsAbs(env["HOME"]) {
			t.Errorf("%s node HOME %q is not absolute", name, env["HOME"])
		}
	}

	for _, name := range []string{"AGENT_PEER_TEST_RUNTIME_ROOT", "AGENT_PEER_TEST_HOME", "HOME"} {
		if firstEnv[name] == secondEnv[name] {
			t.Errorf("two nodes share %s=%q", name, firstEnv[name])
		}
	}
}

func TestLocalHostCleanupKillsDetached(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("looking up processes by environment scans /proc")
	}

	node := NewLocalHost(t, LocalOptions{Name: "cleanup"})
	local := node.impl.(*localNode)

	// The shell exits at once; setsid detaches the sleep, which is reparented
	// and outlives the process that started it, like a Broker started by an
	// MCP server process.
	cmd := exec.Command("sh", "-c", "setsid sleep 60 >/dev/null 2>&1 & echo $!")
	cmd.Env = local.env.Build(nil)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("starting a detached process: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("reading the detached process ID from %q: %v", out, err)
	}

	Eventually(t, 5*time.Second, "the detached process starts", func() (bool, string) {
		pids := procsWithEnv("AGENT_PEER_TEST_RUNTIME_ROOT", local.root)
		if containsPID(pids, pid) {
			return true, fmt.Sprintf("pid %d is running", pid)
		}
		return false, fmt.Sprintf("pid %d not found among %v", pid, pids)
	})

	local.cleanup()

	Eventually(t, 5*time.Second, "cleanup kills the detached process", func() (bool, string) {
		pids := procsWithEnv("AGENT_PEER_TEST_RUNTIME_ROOT", local.root)
		if containsPID(pids, pid) {
			return false, fmt.Sprintf("pid %d still running", pid)
		}
		if len(pids) != 0 {
			return false, fmt.Sprintf("processes %v still carry the runtime root", pids)
		}
		return true, ""
	})
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}

func TestFreeAddrIsLoopback(t *testing.T) {
	node := NewLocalHost(t, LocalOptions{Name: "addr"})

	for i := 0; i < 3; i++ {
		addr := node.FreeAddr(t)
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Fatalf("FreeAddr returned %q, want a 127.0.0.1 address", addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("splitting %q: %v", addr, err)
		}
		if host != "127.0.0.1" {
			t.Errorf("FreeAddr returned host %q, want 127.0.0.1", host)
		}
		if number, err := strconv.Atoi(port); err != nil || number == 0 {
			t.Errorf("FreeAddr returned port %q, want a non-zero number", port)
		}
	}
}
