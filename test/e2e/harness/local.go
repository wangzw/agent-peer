package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	"github.com/wangzw/agent-peer/test/e2e/node"
)

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
	broker        *node.BrokerProc
	brokerLogPath string
	brokers       int
	freeAddrs     map[string]bool
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
		freeAddrs: map[string]bool{},
	}
	t.Cleanup(l.cleanup)
	return &Node{impl: l}
}

// brokerSocket is the local socket of the node's Broker.
func (l *localNode) brokerSocket() string {
	return filepath.Join(l.root, strconv.Itoa(os.Geteuid()), "broker.sock")
}

// configPath is the account's configuration file.
func (l *localNode) configPath() string {
	return filepath.Join(l.acct, ".agent-peer", "config.yaml")
}

func (l *localNode) accountHome() string { return l.acct }

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
	l.mu.Lock()
	l.freeAddrs[addr] = true
	l.mu.Unlock()
	return addr
}

func (l *localNode) writeConfig(t testing.TB, yaml string) {
	t.Helper()
	dir := filepath.Join(l.acct, ".agent-peer")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("harness: node %s: creating config dir %s: %v", l.name, dir, err)
		return
	}

	// A Broker may be watching the directory, so write a complete file and
	// rename it into place; a reader never sees a half-written configuration.
	tmp, err := os.CreateTemp(dir, "config-*.yaml")
	if err != nil {
		t.Fatalf("harness: node %s: creating temporary config: %v", l.name, err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(yaml); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		t.Fatalf("harness: node %s: writing %s: %v", l.name, tmpName, err)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		t.Fatalf("harness: node %s: closing %s: %v", l.name, tmpName, err)
		return
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		t.Fatalf("harness: node %s: setting mode of %s: %v", l.name, tmpName, err)
		return
	}
	if err := os.Rename(tmpName, l.configPath()); err != nil {
		_ = os.Remove(tmpName)
		t.Fatalf("harness: node %s: installing %s: %v", l.name, l.configPath(), err)
	}
}

func (l *localNode) readConfig(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(l.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("harness: node %s: reading %s: %v", l.name, l.configPath(), err)
		return nil
	}
	return data
}

// readConfigMaybe returns the configuration file without failing the test.
func (l *localNode) readConfigMaybe() ([]byte, bool) {
	data, err := os.ReadFile(l.configPath())
	if err != nil {
		return nil, false
	}
	return data, true
}

func (l *localNode) readFile(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("harness: node %s: reading %s: %v", l.name, path, err)
		return nil
	}
	return data
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

	// StartSession completes the MCP initialize handshake before it returns;
	// the deadline here is the project-wide test wait, not the thirty-second
	// startup allowance Claude Code itself uses.
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()

	session, err := node.StartSession(ctx, tb, l.env, node.MCPCommand{Path: l.bin, Args: []string{"mcp"}}, spec)
	if err != nil {
		tb.Fatalf("harness: node %s: starting session %s: %v", l.name, spec.Name, err)
		return nil
	}

	s := &Session{
		name: spec.Name,
		impl: &localSession{name: spec.Name, session: session},
	}
	l.mu.Lock()
	l.sessions = append(l.sessions, s)
	l.mu.Unlock()
	return s
}

// localSession adapts node.Session to the harness session interface, turning
// errors into test failures.
type localSession struct {
	name    string
	session *node.Session
}

func (s *localSession) call(t testing.TB, tool string, args map[string]any) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sessionCallBound)
	defer cancel()
	result, err := s.session.Call(ctx, tool, args)
	if err != nil {
		t.Fatalf("harness: session %s: calling %s: %v", s.name, tool, err)
		return Result{}
	}
	return Result{ToolResult: result}
}

func (s *localSession) tools(t testing.TB) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	names, err := s.session.Tools(ctx)
	if err != nil {
		t.Fatalf("harness: session %s: listing tools: %v", s.name, err)
		return nil
	}
	return names
}

func (s *localSession) stopMCP(t testing.TB, sig syscall.Signal) int {
	t.Helper()
	code, err := s.session.StopMCP(sig)
	if err != nil {
		t.Fatalf("harness: session %s: stopping the MCP server process: %v", s.name, err)
		return -1
	}
	return code
}

func (s *localSession) restartMCP(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	if err := s.session.RestartMCP(ctx); err != nil {
		t.Fatalf("harness: session %s: restarting the MCP server process: %v", s.name, err)
	}
}

func (s *localSession) startExtraMCP(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	if _, err := s.session.StartExtraMCP(ctx); err != nil {
		t.Fatalf("harness: session %s: starting an extra MCP server process: %v", s.name, err)
	}
}

func (s *localSession) inboxConnections(t testing.TB) int {
	t.Helper()
	count, err := s.session.InboxConnections()
	if err != nil {
		t.Fatalf("harness: session %s: counting inbox connections: %v", s.name, err)
		return 0
	}
	return count
}

func (s *localSession) fake() FakeSession { return s.session.Fake() }

func (s *localSession) end() error { return s.session.End() }

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
			if err := s.endIfNotEnded(); err != nil {
				l.t.Errorf("harness: node %s: ending session %s: %v", l.name, s.name, err)
			}
		}
		for _, o := range oracles {
			o.close()
		}
		if broker != nil {
			terminateBroker(broker)
		}

		l.killProcesses()

		if err := os.RemoveAll(l.root); err != nil {
			l.t.Errorf("harness: node %s: removing runtime root %s: %v", l.name, l.root, err)
		}
	})
}

// terminateBroker asks the Broker to exit with SIGTERM and kills it when it has
// not exited within two seconds.
func terminateBroker(b *node.BrokerProc) {
	_ = b.Signal(syscall.SIGTERM)
	if _, err := b.Wait(2 * time.Second); err == nil {
		return
	}
	_ = b.Signal(syscall.SIGKILL)
	_, _ = b.Wait(2 * time.Second)
}

// killProcesses kills every process whose environment carries this node's
// runtime root, including a Broker that detached from the process that started
// it. The scan is the node library's; no second implementation lives here.
func (l *localNode) killProcesses() {
	procs, err := node.ListProcs(func(p node.Proc) bool {
		return p.Env["AGENT_PEER_TEST_RUNTIME_ROOT"] == l.root
	})
	if err != nil {
		// Other platforms cannot scan processes; the processes the node
		// started itself have already been stopped above.
		if !errors.Is(err, errors.ErrUnsupported) {
			l.t.Errorf("harness: node %s: listing processes: %v", l.name, err)
		}
		return
	}
	self := os.Getpid()
	for _, proc := range procs {
		if proc.PID == self {
			continue
		}
		_ = syscall.Kill(proc.PID, syscall.SIGKILL)
	}
}

// logHasAddressInUse reports whether the Broker log contains the failure that
// the one retry accepts as a port race.
func logHasAddressInUse(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "address already in use")
}

// replaceFreeAddrs replaces every address this node handed out through
// FreeAddr that appears in args or configText with a fresh one. Addresses the
// test obtained elsewhere are left alone, so a scenario that deliberately
// occupies a port still sees the Broker fail.
func (l *localNode) replaceFreeAddrs(tb testing.TB, args []string, configText string) ([]string, string, bool) {
	l.mu.Lock()
	addrs := make([]string, 0, len(l.freeAddrs))
	for addr := range l.freeAddrs {
		addrs = append(addrs, addr)
	}
	l.mu.Unlock()
	// Longer addresses first, so an address that is a prefix of another is not
	// replaced inside it.
	sort.Slice(addrs, func(i, j int) bool { return len(addrs[i]) > len(addrs[j]) })

	replacements := map[string]string{}
	changed := false
	replace := func(text string) string {
		for _, old := range addrs {
			if !strings.Contains(text, old) {
				continue
			}
			fresh, ok := replacements[old]
			if !ok {
				fresh = l.freeAddr(tb)
				replacements[old] = fresh
			}
			text = strings.ReplaceAll(text, old, fresh)
			changed = true
		}
		return text
	}

	newArgs := make([]string, len(args))
	for i, arg := range args {
		newArgs[i] = replace(arg)
	}
	return newArgs, replace(configText), changed
}

func (l *localNode) startBroker(tb testing.TB, spec BrokerSpec) BrokerStart {
	tb.Helper()

	l.mu.Lock()
	if l.broker != nil {
		l.mu.Unlock()
		tb.Fatalf("harness: node %s: a Broker is already running", l.name)
		return BrokerStart{}
	}
	l.mu.Unlock()

	args := append([]string(nil), spec.Args...)
	configText := spec.ConfigYAML
	if configText == "" {
		// Retrying rewrites the file the Broker is about to read, so keep its
		// current contents at hand even when the caller did not pass any.
		if data, ok := l.readConfigMaybe(); ok {
			configText = string(data)
		}
	}
	if spec.ConfigYAML != "" {
		l.writeConfig(tb, spec.ConfigYAML)
	}

	for attempt := 0; ; attempt++ {
		l.mu.Lock()
		l.brokers++
		logPath := filepath.Join(l.root, fmt.Sprintf("broker-%d.log", l.brokers))
		l.brokerLogPath = logPath
		l.mu.Unlock()

		proc, start, err := node.StartBroker(context.Background(), node.BrokerSpec{
			Path:    l.bin,
			Args:    args,
			Env:     l.env.Build(spec.Env),
			LogPath: logPath,
			Socket:  l.brokerSocket(),
		}, spec.Bound)
		if err != nil {
			tb.Fatalf("harness: node %s: starting Broker: %v", l.name, err)
			return BrokerStart{}
		}
		if start.Running {
			l.mu.Lock()
			l.broker = proc
			l.mu.Unlock()
			return start
		}

		// A port handed out by FreeAddr can be taken between the probe and the
		// bind. Accept that race once: move every such address to a new one,
		// rewrite the configuration file, and start again.
		if attempt == 0 && logHasAddressInUse(logPath) {
			newArgs, newConfig, changed := l.replaceFreeAddrs(tb, args, configText)
			if changed {
				if newConfig != "" {
					l.writeConfig(tb, newConfig)
				}
				args = newArgs
				configText = newConfig
				continue
			}
		}
		return start
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
	_ = broker.Signal(sig)
	code, err := broker.Wait(10 * time.Second)
	if err != nil {
		_ = broker.Signal(syscall.SIGKILL)
		code, _ = broker.Wait(2 * time.Second)
	}

	l.mu.Lock()
	if l.broker == broker {
		l.broker = nil
	}
	l.mu.Unlock()
	return code
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

func (l *localNode) oracle() *OracleClient {
	oracle, err := node.DialOracle(l.brokerSocket())
	if err != nil {
		l.t.Fatalf("harness: node %s: connecting to the local socket: %v", l.name, err)
		return nil
	}
	o := &OracleClient{oracle: oracle}
	l.mu.Lock()
	l.oracles = append(l.oracles, o)
	l.mu.Unlock()
	return o
}
