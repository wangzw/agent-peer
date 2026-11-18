package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// testBinary is the agent-peer binary built once by TestMain. Subprocess tests
// run it as a child process instead of calling the package's functions, so the
// exit codes, the standard streams and the signal handling are the real ones.
var testBinary string

// waitDelay bounds how long Wait waits for the child's I/O pipes after the
// process has exited, so a grandchild that inherited one of them cannot hang
// the test harness. It is also the backstop after the SIGKILL fallback in stop.
const waitDelay = 3 * time.Second

// TestMain builds the binary every subprocess test runs. The build uses
// os.Environ because go build is a tool here, not the process under test; the
// process under test gets a clean environment from sandbox.env.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-peer-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create binary directory: %v\n", err)
		os.Exit(1)
	}
	testBinary = filepath.Join(dir, "agent-peer")

	build := exec.Command("go", "build",
		"-tags", "agentpeer_testenv",
		"-ldflags", "-X main.version=0.0.0-cmdtest",
		"-o", testBinary,
		".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build test binary: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// sandbox is one isolated account environment for a subprocess test. The
// runtime root comes from os.MkdirTemp rather than t.TempDir: t.TempDir embeds
// the test name and, on macOS, lives under /var/folders, so "<root>/<euid>/
// broker.sock" would exceed the 104-byte Unix socket path limit.
type sandbox struct {
	t    *testing.T
	root string // value of AGENT_PEER_TEST_RUNTIME_ROOT
	home string // value of AGENT_PEER_TEST_HOME and HOME

	// extraEnv is appended to env for every child started from this sandbox.
	// Tests use it for the variables the child needs beyond the fixed four,
	// such as AGENT_PEER_SOCKET or CLAUDE_CODE_MESSAGING_SOCKET.
	extraEnv []string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()

	root, err := os.MkdirTemp("", "ap-")
	if err != nil {
		t.Fatalf("create runtime root: %v", err)
	}
	// os.MkdirTemp already creates the directory with 0700; set it again so a
	// restrictive umask or a platform default cannot widen it.
	if err := os.Chmod(root, 0o700); err != nil {
		os.RemoveAll(root)
		t.Fatalf("chmod runtime root: %v", err)
	}

	sb := &sandbox{t: t, root: root, home: t.TempDir()}
	t.Cleanup(func() { os.RemoveAll(root) })
	return sb
}

// env is the child environment. It starts from nothing rather than
// os.Environ so the session variables of the test runner (for example
// CLAUDE_CODE_MESSAGING_SOCKET or AGENT_PEER_SOCKET) cannot leak into the
// process under test.
func (s *sandbox) env() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + s.home,
		"AGENT_PEER_TEST_RUNTIME_ROOT=" + s.root,
		"AGENT_PEER_TEST_HOME=" + s.home,
	}
	return append(env, s.extraEnv...)
}

// runtimeDir mirrors the config and mcpserver test hooks: the superuser's
// directory is root/root, everyone else's is root/<euid>.
func (s *sandbox) runtimeDir() string {
	if euid := os.Geteuid(); euid == 0 {
		return filepath.Join(s.root, "root")
	}
	return filepath.Join(s.root, strconv.Itoa(os.Geteuid()))
}

func (s *sandbox) configPath() string {
	return filepath.Join(s.home, ".agent-peer", "config.yaml")
}

// writeConfig writes the account configuration. The account home is selected
// through AGENT_PEER_TEST_HOME, not HOME, so this does not depend on the child
// honoring HOME.
func (s *sandbox) writeConfig(yaml string) {
	s.t.Helper()

	dir := filepath.Dir(s.configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(s.configPath(), []byte(yaml), 0o600); err != nil {
		s.t.Fatalf("write config: %v", err)
	}
}

// occupiedPort returns a port that stays bound for the lifetime of the
// listener, so the Broker under test cannot bind it.
func occupiedPort(t *testing.T) (int, net.Listener) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on an ephemeral port: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().(*net.TCPAddr).Port, l
}

// listenAddr formats a loopback address for the Broker to bind or the test to
// dial.
func listenAddr(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}

// freePort returns a port that was bound and released, so the Broker under
// test can take it. There is an unavoidable gap between the close and the
// Broker's bind, which is why tests prefer occupiedPort when the point is that
// binding must fail.
func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on an ephemeral port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close ephemeral listener: %v", err)
	}
	return port
}

// freePorts returns n distinct ports that were bound at the same time and then
// released together. Binding every listener before closing any guarantees the
// ports differ, which two separate freePort calls cannot.
func freePorts(t *testing.T, n int) []int {
	t.Helper()

	listeners := make([]net.Listener, n)
	ports := make([]int, n)
	for i := range listeners {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, open := range listeners[:i] {
				_ = open.Close()
			}
			t.Fatalf("listen on an ephemeral port: %v", err)
		}
		listeners[i] = l
		ports[i] = l.Addr().(*net.TCPAddr).Port
	}
	for _, l := range listeners {
		if err := l.Close(); err != nil {
			t.Fatalf("close ephemeral listener: %v", err)
		}
	}
	return ports
}

// lockedBuffer is the standard error sink for a child process. os/exec copies
// the child's output from another goroutine, and tests read the buffer while
// the child may still write, so every access is guarded.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// brokerProc is one Broker child process started by startBroker.
type brokerProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	ready  *os.File
	reader *bufio.Reader
	stderr *lockedBuffer

	exited  chan struct{}
	waitErr error
}

// startBroker runs "agent-peer broker" with a readiness pipe on fd 3. The
// caller reads the pipe with readyLine and collects the exit code with wait.
func startBroker(t *testing.T, sb *sandbox, args ...string) *brokerProc {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create readiness pipe: %v", err)
	}

	cmdArgs := append([]string{"broker"}, args...)
	cmdArgs = append(cmdArgs, "--ready-fd=3")
	cmd := exec.Command(testBinary, cmdArgs...)
	cmd.Env = sb.env()
	cmd.WaitDelay = waitDelay
	// ExtraFiles are inherited starting at fd 3, matching the --ready-fd=3
	// argument and mcpserver.ReadyFD.
	cmd.ExtraFiles = []*os.File{w}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		t.Fatalf("start broker: %v", err)
	}
	// The child holds the write end as fd 3. Closing ours turns an exiting
	// Broker into an EOF on the read end instead of a wait for the timeout.
	w.Close()

	p := &brokerProc{
		t:      t,
		cmd:    cmd,
		ready:  r,
		reader: bufio.NewReader(r),
		stderr: stderr,
		exited: make(chan struct{}),
	}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(p.stop)
	return p
}

// readyLine reads one line from the readiness pipe, failing the test when none
// arrives in time.
func (p *brokerProc) readyLine(timeout time.Duration) string {
	p.t.Helper()

	deadline := time.Now().Add(timeout)
	if err := p.ready.SetReadDeadline(deadline); err != nil {
		p.t.Fatalf("set readiness deadline: %v", err)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil {
		// A closed pipe usually means the process is exiting. Wait for the same
		// deadline rather than adding a fixed delay, so the whole call stays
		// within timeout; the failure message can then carry the exit code and
		// the complete standard error instead of racing the copy goroutine.
		select {
		case <-p.exited:
		case <-time.After(time.Until(deadline)):
		}
		state := "still running"
		select {
		case <-p.exited:
			state = fmt.Sprintf("exit code %d (%v)", p.cmd.ProcessState.ExitCode(), p.waitErr)
		default:
		}
		p.t.Fatalf("read readiness line: %v (process: %s; stderr: %s)", err, state, p.stderr.String())
	}
	return strings.TrimRight(line, "\r\n")
}

// wait waits for the process to exit and returns its exit code.
func (p *brokerProc) wait(timeout time.Duration) int {
	p.t.Helper()

	select {
	case <-p.exited:
	case <-time.After(timeout):
		p.t.Fatalf("broker did not exit within %s (stderr: %s)", timeout, p.stderr.String())
	}
	return p.cmd.ProcessState.ExitCode()
}

// lastStderrLine returns the last line of the Broker's standard error that
// starts with prefix, or "" when there is none. The Broker also logs JSON
// runtime records to standard error, so tests select the failure line by
// prefix.
func (p *brokerProc) lastStderrLine(prefix string) string {
	text := strings.TrimRight(p.stderr.String(), "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], prefix) {
			return lines[i]
		}
	}
	return ""
}

// stop is the cleanup registered by startBroker: terminate a still running
// Broker with SIGTERM and fall back to SIGKILL after three seconds.
func (p *brokerProc) stop() {
	if p.ready != nil {
		_ = p.ready.Close()
	}

	select {
	case <-p.exited:
		return
	default:
	}

	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(3 * time.Second):
	}
	_ = p.cmd.Process.Kill()
	// WaitDelay bounds the wait for the child's pipes; a process that still
	// refuses to exit after SIGKILL is reported instead of being signalled
	// again by pid.
	select {
	case <-p.exited:
	case <-time.After(waitDelay):
		p.t.Errorf("broker process %d did not exit after SIGKILL", p.cmd.Process.Pid)
	}
}
