package harness

import (
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
	"google.golang.org/grpc"
)

// LocalOptions configures a node that runs its processes directly on the
// machine running the tests.
type LocalOptions struct {
	// Name names the node in logs and artifact file names.
	Name string
}

// SessionSpec describes one simulated Claude Code session. Most fields map
// directly to fakesession.Options; the rest select how the MCP server process
// is started.
type SessionSpec struct {
	// Name and Status are written to the session record.
	Name, Status string

	// ConfigDir is the Claude configuration directory. Empty means
	// $HOME/.claude.
	ConfigDir string

	// ExportConfigDir exports CLAUDE_CONFIG_DIR=ConfigDir to the MCP server
	// process.
	ExportConfigDir bool

	// Workdir is the working directory of the MCP server process. Empty means
	// $HOME.
	Workdir string

	// Mode, BusyDelay, NoBind and SocketPath are passed to fakesession.
	Mode       fakesession.Mode
	BusyDelay  time.Duration
	NoBind     bool
	SocketPath string

	// NoSocketEnv suppresses CLAUDE_CODE_MESSAGING_SOCKET in the MCP server
	// process environment.
	NoSocketEnv bool

	// Env adds environment variables to the MCP server process, on top of the
	// node's BaseEnv.
	Env map[string]string

	// StartMCP starts the MCP server process together with the simulated
	// session.
	StartMCP bool
}

// BrokerSpec configures the Broker started by Node.StartBroker.
//
// The plan revision will add the fields that pass extra environment variables,
// --listen arguments and a configuration file. They are deliberately absent
// until that revision lands, so the Broker currently starts from the node's
// isolated environment alone.
type BrokerSpec struct {
	// Bound bounds the readiness wait. Zero means 13s, the 10s readiness pipe
	// bound plus 3s.
	Bound time.Duration
}

// BrokerStart reports the outcome of Node.StartBroker.
type BrokerStart struct {
	// Running reports whether the Broker answered the readiness probe.
	Running bool

	// PID is the Broker process ID when Running is true.
	PID int

	// ExitCode is the process exit code when Running is false.
	ExitCode int

	// StderrLast is the last line of the Broker log that starts with
	// "agent-peer broker: ", with that prefix removed.
	StderrLast string
}

// ExecResult is the outcome of Node.Exec.
type ExecResult struct {
	ExitCode       int
	Stdout, Stderr string
}

// OracleClient reads Broker state over the local socket, without an Agent
// identity.
type OracleClient struct {
	conn   *grpc.ClientConn
	client localpb.LocalClient
}

// Hello calls Local.Hello on the Broker.
func (o *OracleClient) Hello(t testing.TB) *localpb.HelloResponse {
	t.Helper()
	resp, err := o.hello()
	if err != nil {
		t.Fatalf("harness: calling Local.Hello: %v", err)
		return nil
	}
	return resp
}

// close releases the underlying connection.
func (o *OracleClient) close() {
	_ = o.conn.Close()
}

// backend is what a Node delegates to. The local backend runs processes
// directly on the test host; the container backend, added with the container
// node work item, talks to a node agent in a container.
type backend interface {
	startBroker(t testing.TB, spec BrokerSpec) BrokerStart
	stopBroker(t testing.TB, sig syscall.Signal) int
	brokerLog(t testing.TB) []byte
	startSession(t testing.TB, spec SessionSpec) *Session
	exec(t testing.TB, argv []string, env map[string]string) ExecResult
	oracle() *OracleClient
	freeAddr(t testing.TB) string
}

// Node is one host in a scenario. Every method reports infrastructure errors
// with t.Fatalf.
type Node struct {
	impl backend
}

// StartBroker starts a Broker on the node and waits until it is ready.
func (n *Node) StartBroker(t testing.TB, spec BrokerSpec) BrokerStart {
	t.Helper()
	return n.impl.startBroker(t, spec)
}

// StopBroker sends sig to the current Broker, waits for it to exit and returns
// its exit code.
func (n *Node) StopBroker(t testing.TB, sig syscall.Signal) int {
	t.Helper()
	return n.impl.stopBroker(t, sig)
}

// BrokerLog returns the log of the most recently started Broker.
func (n *Node) BrokerLog(t testing.TB) []byte {
	t.Helper()
	return n.impl.brokerLog(t)
}

// StartSession starts one simulated Claude Code session on the node.
func (n *Node) StartSession(t testing.TB, spec SessionSpec) *Session {
	t.Helper()
	return n.impl.startSession(t, spec)
}

// Exec runs argv once as the node's account and returns its output and exit
// code.
func (n *Node) Exec(t testing.TB, argv []string, env map[string]string) ExecResult {
	t.Helper()
	return n.impl.exec(t, argv, env)
}

// Oracle returns a client for the local socket of the node's Broker.
func (n *Node) Oracle() *OracleClient {
	return n.impl.oracle()
}

// FreeAddr reserves an unused loopback address and returns it as
// 127.0.0.1:<port>.
func (n *Node) FreeAddr(t testing.TB) string {
	t.Helper()
	return n.impl.freeAddr(t)
}

// Session is one simulated Claude Code session: a fakesession.Fake plus,
// optionally, the MCP server process driving it.
type Session struct {
	name string
	fake *fakesession.Fake
	mcp  *mcpProc
}

// Fake returns the simulated session.
func (s *Session) Fake() *fakesession.Fake {
	return s.fake
}

// MCPPID returns the process ID of the MCP server process, or 0 when the
// session was started without one.
func (s *Session) MCPPID() int {
	if s.mcp == nil {
		return 0
	}
	return s.mcp.pid
}

// WaitFrame waits up to bound for the next frame delivered to the session's
// inbox and fails the test when none arrives.
func (s *Session) WaitFrame(t testing.TB, bound time.Duration) fakesession.Frame {
	t.Helper()
	frame, ok := s.fake.WaitFrame(bound)
	if !ok {
		t.Fatalf("harness: session %s: no inbox frame within %s", s.name, bound)
	}
	return frame
}
