package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// BaseEnv is the complete environment of every child process a local node
// starts. Nothing is inherited from os.Environ; the local node isolates itself
// from the developer's account through AGENT_PEER_TEST_RUNTIME_ROOT and
// AGENT_PEER_TEST_HOME instead.
type BaseEnv struct {
	// Home becomes HOME.
	Home string
	// Path becomes PATH.
	Path string
	// Extra adds variables, for example AGENT_PEER_TEST_RUNTIME_ROOT.
	Extra map[string]string
}

// Build returns the environment as NAME=value strings, sorted by name. A value
// of "" in more removes the variable. LANG is always C.UTF-8.
func (e BaseEnv) Build(more map[string]string) []string {
	values := map[string]string{
		"HOME": e.Home,
		"PATH": e.Path,
		"LANG": "C.UTF-8",
	}
	for name, value := range e.Extra {
		setEnv(values, name, value)
	}
	for name, value := range more {
		setEnv(values, name, value)
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env
}

// setEnv sets name in values, or deletes it when value is empty.
func setEnv(values map[string]string, name, value string) {
	if value == "" {
		delete(values, name)
		return
	}
	values[name] = value
}

// brokerReadyBound is the default bound for a Broker readiness wait.
const brokerReadyBound = 13 * time.Second

// mcpProc is one MCP server process started for a session.
//
// The MCP protocol client that drives the process (the handshake, tools/list
// and tools/call) belongs to the node session driver. Until that work item
// lands the process is started with the correct environment and working
// directory, and its stdin is kept open, but no protocol exchange happens.
type mcpProc struct {
	cmd   *exec.Cmd
	pid   int
	log   *os.File
	stdin io.WriteCloser

	done chan struct{}
	err  error
}

// localNode runs a node's processes directly on the test host.
type localNode struct {
	t    *testing.T
	name string
	bin  string
	root string
	acct string
	home string
	env  BaseEnv

	cleanupOnce sync.Once

	mu            sync.Mutex
	sessions      []*Session
	oracles       []*OracleClient
	broker        *brokerProc
	brokerLogPath string
	brokers       int
	mcpStarted    int
}

// NewLocalHost starts a node that runs on the test host. Its runtime root and
// account home are fresh temporary directories, so it shares neither the
// account lock nor ~/.agent-peer with the developer or with another node. All
// processes and directories are removed when the test ends.
func NewLocalHost(t *testing.T, opts LocalOptions) *Node {
	t.Helper()

	bin := BuildBinary(t, runtime.GOOS, runtime.GOARCH, "agentpeer_testenv")

	// A short path keeps the Broker's Unix socket under the length limit.
	root, err := os.MkdirTemp("/tmp", "ape2e")
	if err != nil {
		t.Fatalf("harness: creating runtime root: %v", err)
	}
	acct := t.TempDir()
	home := t.TempDir()

	l := &localNode{
		t:    t,
		name: opts.Name,
		bin:  bin,
		root: root,
		acct: acct,
		home: home,
		env: BaseEnv{
			Home: home,
			Path: os.Getenv("PATH"),
			Extra: map[string]string{
				"AGENT_PEER_TEST_RUNTIME_ROOT": root,
				"AGENT_PEER_TEST_HOME":         acct,
			},
		},
	}
	t.Cleanup(l.cleanup)
	return &Node{impl: l}
}

// brokerSocket is the local socket of the node's Broker.
func (l *localNode) brokerSocket() string {
	return filepath.Join(l.root, strconv.Itoa(os.Geteuid()), "broker.sock")
}

func (l *localNode) freeAddr(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: node %s: reserving a free address: %v", l.name, err)
		return ""
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("harness: node %s: releasing %s: %v", l.name, addr, err)
	}
	return addr
}

func (l *localNode) exec(t testing.TB, argv []string, env map[string]string) ExecResult {
	t.Helper()
	if len(argv) == 0 {
		t.Fatalf("harness: node %s: Exec needs a command", l.name)
		return ExecResult{}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = l.home
	cmd.Env = l.env.Build(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := ExecResult{}
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		result.ExitCode = 0
	case errors.As(err, &exitErr):
		result.ExitCode = exitErr.ExitCode()
	default:
		t.Fatalf("harness: node %s: running %v: %v", l.name, argv, err)
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	return result
}

func (l *localNode) startSession(tb testing.TB, spec SessionSpec) *Session {
	tb.Helper()

	configDir := spec.ConfigDir
	if configDir == "" {
		configDir = filepath.Join(l.env.Home, ".claude")
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		tb.Fatalf("harness: node %s: creating config dir %s: %v", l.name, configDir, err)
		return nil
	}

	fake := fakesession.New(tb, fakesession.Options{
		ConfigDir:  configDir,
		Name:       spec.Name,
		Status:     spec.Status,
		Mode:       spec.Mode,
		BusyDelay:  spec.BusyDelay,
		NoBind:     spec.NoBind,
		SocketPath: spec.SocketPath,
	})

	s := &Session{name: spec.Name, fake: fake}
	if spec.StartMCP {
		s.mcp = l.startMCP(tb, s, spec)
	}

	l.mu.Lock()
	l.sessions = append(l.sessions, s)
	l.mu.Unlock()
	return s
}

// startMCP starts the MCP server process for s. See mcpProc for what is and is
// not implemented before the node session driver lands.
func (l *localNode) startMCP(tb testing.TB, s *Session, spec SessionSpec) *mcpProc {
	tb.Helper()

	l.mu.Lock()
	l.mcpStarted++
	n := l.mcpStarted
	l.mu.Unlock()

	logPath := filepath.Join(l.env.Home, fmt.Sprintf("mcp-%d-%d.log", s.fake.PID(), n))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		tb.Fatalf("harness: node %s: opening MCP log %s: %v", l.name, logPath, err)
		return nil
	}

	more := make(map[string]string, len(spec.Env)+2)
	if !spec.NoSocketEnv {
		more["CLAUDE_CODE_MESSAGING_SOCKET"] = s.fake.SocketPath()
	}
	if spec.ExportConfigDir {
		more["CLAUDE_CONFIG_DIR"] = spec.ConfigDir
	}
	for name, value := range spec.Env {
		more[name] = value
	}

	workdir := spec.Workdir
	if workdir == "" {
		workdir = l.env.Home
	}

	cmd := exec.Command(l.bin, "mcp")
	cmd.Dir = workdir
	cmd.Env = l.env.Build(more)
	cmd.Stderr = logFile
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		tb.Fatalf("harness: node %s: creating MCP stdin: %v", l.name, err)
		return nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		tb.Fatalf("harness: node %s: creating MCP stdout: %v", l.name, err)
		return nil
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		tb.Fatalf("harness: node %s: starting MCP server: %v", l.name, err)
		return nil
	}

	p := &mcpProc{
		cmd:   cmd,
		pid:   cmd.Process.Pid,
		log:   logFile,
		stdin: stdin,
		done:  make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(logFile, stdout)
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p
}

// stop ends the MCP server process and releases the log.
func (p *mcpProc) stop() {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGKILL)
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
	if p.log != nil {
		_ = p.log.Close()
	}
}

// shutdownSession closes the simulated session and stops its MCP server
// process. It is the cleanup form of the session end method, which the plan
// revision still has to define.
func (l *localNode) shutdownSession(s *Session) {
	if s.mcp != nil {
		s.mcp.stop()
	}
	s.fake.RemoveRecord()
	s.fake.Close()
}

// cleanup ends the node's sessions, stops its Broker, kills every process that
// still carries this node's runtime root and removes the root. It is
// idempotent.
func (l *localNode) cleanup() {
	l.cleanupOnce.Do(func() {
		l.mu.Lock()
		sessions := l.sessions
		l.sessions = nil
		broker := l.broker
		l.broker = nil
		oracles := l.oracles
		l.oracles = nil
		l.mu.Unlock()

		for _, s := range sessions {
			l.shutdownSession(s)
		}
		for _, o := range oracles {
			o.close()
		}
		if broker != nil {
			broker.stop(2 * time.Second)
		}

		for _, pid := range procsWithEnv("AGENT_PEER_TEST_RUNTIME_ROOT", l.root) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if err := os.RemoveAll(l.root); err != nil {
			l.t.Errorf("harness: node %s: removing runtime root %s: %v", l.name, l.root, err)
		}
	})
}

// procsWithEnv returns the process IDs whose environment contains
// name=value. It scans /proc and therefore works only on Linux; elsewhere it
// returns nil.
func procsWithEnv(name, value string) []int {
	if runtime.GOOS != "linux" {
		return nil
	}
	want := name + "=" + value
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, variable := range strings.Split(string(environ), "\x00") {
			if variable == want {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// brokerProc is a Broker process started by startBroker.
type brokerProc struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu  sync.Mutex
	err error
}

func newBrokerProc(cmd *exec.Cmd) *brokerProc {
	b := &brokerProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		b.mu.Lock()
		b.err = cmd.Wait()
		b.mu.Unlock()
		close(b.done)
	}()
	return b
}

func (b *brokerProc) finished() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

func (b *brokerProc) signal(sig syscall.Signal) error {
	return b.cmd.Process.Signal(sig)
}

// wait waits up to bound for the process to exit and reports whether it did.
func (b *brokerProc) wait(bound time.Duration) bool {
	select {
	case <-b.done:
		return true
	case <-time.After(bound):
		return false
	}
}

func (b *brokerProc) exitCode() int {
	b.mu.Lock()
	err := b.err
	b.mu.Unlock()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		return -1
	}
}

// stop asks the process to exit with SIGTERM and kills it if it has not exited
// within bound.
func (b *brokerProc) stop(bound time.Duration) {
	_ = b.signal(syscall.SIGTERM)
	if !b.wait(bound) {
		_ = b.signal(syscall.SIGKILL)
		b.wait(bound)
	}
}

func (l *localNode) startBroker(tb testing.TB, spec BrokerSpec) BrokerStart {
	tb.Helper()

	l.mu.Lock()
	if l.broker != nil {
		l.mu.Unlock()
		tb.Fatalf("harness: node %s: a Broker is already running", l.name)
		return BrokerStart{}
	}
	l.brokers++
	logPath := filepath.Join(l.root, fmt.Sprintf("broker-%d.log", l.brokers))
	l.brokerLogPath = logPath
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.mu.Unlock()
		tb.Fatalf("harness: node %s: opening Broker log %s: %v", l.name, logPath, err)
		return BrokerStart{}
	}

	cmd := exec.Command(l.bin, "broker")
	cmd.Env = l.env.Build(nil)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		l.mu.Unlock()
		_ = logFile.Close()
		tb.Fatalf("harness: node %s: starting Broker: %v", l.name, err)
		return BrokerStart{}
	}
	// The child has its own descriptor; the parent does not need this one.
	_ = logFile.Close()

	broker := newBrokerProc(cmd)
	l.broker = broker
	l.mu.Unlock()

	bound := spec.Bound
	if bound <= 0 {
		bound = brokerReadyBound
	}
	deadline := time.Now().Add(bound)
	socket := l.brokerSocket()
	for {
		if broker.finished() {
			l.mu.Lock()
			l.broker = nil
			l.mu.Unlock()
			return BrokerStart{
				Running:    false,
				ExitCode:   broker.exitCode(),
				StderrLast: lastBrokerError(logPath),
			}
		}
		if brokerReady(socket, 200*time.Millisecond) {
			return BrokerStart{Running: true, PID: cmd.Process.Pid}
		}
		if !time.Now().Before(deadline) {
			broker.stop(2 * time.Second)
			l.mu.Lock()
			l.broker = nil
			l.mu.Unlock()
			tb.Fatalf("harness: node %s: Broker not ready within %s", l.name, bound)
			return BrokerStart{}
		}
		time.Sleep(pollInterval)
	}
}

func (l *localNode) stopBroker(tb testing.TB, sig syscall.Signal) int {
	tb.Helper()

	l.mu.Lock()
	broker := l.broker
	l.mu.Unlock()
	if broker == nil {
		tb.Fatalf("harness: node %s: no Broker is running", l.name)
		return -1
	}

	// A signal to a process that already exited reports an error; the exit
	// code below still tells the caller what happened.
	_ = broker.signal(sig)
	if !broker.wait(10 * time.Second) {
		_ = broker.signal(syscall.SIGKILL)
		broker.wait(2 * time.Second)
	}

	l.mu.Lock()
	if l.broker == broker {
		l.broker = nil
	}
	l.mu.Unlock()
	return broker.exitCode()
}

func (l *localNode) brokerLog(tb testing.TB) []byte {
	tb.Helper()

	l.mu.Lock()
	path := l.brokerLogPath
	l.mu.Unlock()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		tb.Fatalf("harness: node %s: reading Broker log %s: %v", l.name, path, err)
		return nil
	}
	return data
}

// lastBrokerError returns the last line of the Broker log that starts with
// "agent-peer broker: ", with the prefix removed.
func lastBrokerError(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const prefix = "agent-peer broker: "
	last := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			last = strings.TrimPrefix(line, prefix)
		}
	}
	return last
}

// brokerReady reports whether the Broker at socket answers Local.Hello.
func brokerReady(socket string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	_, err = localpb.NewLocalClient(conn).Hello(ctx, &localpb.HelloRequest{ProtocolVersion: localpb.ProtocolVersion})
	return err == nil
}

func (l *localNode) oracle() *OracleClient {
	conn, err := grpc.NewClient("unix://"+l.brokerSocket(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		l.t.Fatalf("harness: node %s: connecting to the local socket: %v", l.name, err)
		return nil
	}
	o := &OracleClient{conn: conn, client: localpb.NewLocalClient(conn)}
	l.mu.Lock()
	l.oracles = append(l.oracles, o)
	l.mu.Unlock()
	return o
}

// hello calls Local.Hello without failing the test.
func (o *OracleClient) hello() (*localpb.HelloResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return o.client.Hello(ctx, &localpb.HelloRequest{ProtocolVersion: localpb.ProtocolVersion})
}
