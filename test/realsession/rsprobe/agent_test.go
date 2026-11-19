package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	ccsock "github.com/PeterSR/claude-code-socket-transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// agentReadyTimeout bounds a simulated Agent's startup. The Agent runs
	// this test binary as its stub MCP server, so a subprocess cold start
	// dominates the wait.
	agentReadyTimeout = 10 * time.Second
	// agentShutdownTimeout bounds a simulated Agent's shutdown after its
	// context is cancelled.
	agentShutdownTimeout = 3 * time.Second
	// agentSendTimeout bounds the library client that writes one frame.
	agentSendTimeout = 3 * time.Second
)

// TestMain turns the test binary into the stand-in MCP server the simulated
// Agent starts as its child, and otherwise runs the tests.
func TestMain(m *testing.M) {
	if os.Getenv("RSPROBE_STUB_MCP") == "1" {
		os.Exit(runStubMCP())
	}
	os.Exit(m.Run())
}

// stubEchoIn is the single argument of the stub echo tool.
type stubEchoIn struct {
	Text string `json:"text"`
}

// runStubMCP serves one tool, echo, which returns the requested text together
// with the session environment variables the Agent passed to the MCP process.
func runStubMCP() int {
	server := mcp.NewServer(&mcp.Implementation{Name: "rsprobe-stub", Version: "0"}, nil)
	mcp.AddTool[stubEchoIn, any](
		server,
		&mcp.Tool{Name: "echo", Description: "Returns the requested text and the messaging environment."},
		func(_ context.Context, _ *mcp.CallToolRequest, in stubEchoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: stubEchoText(in.Text)}}}, nil, nil
		},
	)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		return 1
	}
	return 0
}

// stubEchoText builds the echo tool's answer.
func stubEchoText(text string) string {
	lines := []string{
		text,
		"CLAUDE_CODE_MESSAGING_SOCKET=" + os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"),
		"CLAUDE_CONFIG_DIR=" + os.Getenv("CLAUDE_CONFIG_DIR"),
	}
	if child := os.Getenv("CLAUDE_CODE_CHILD_SESSION"); child != "" {
		lines = append(lines, "CLAUDE_CODE_CHILD_SESSION="+child)
	}
	return strings.Join(lines, "\n")
}

// testAgent is one simulated Agent started in the test process.
type testAgent struct {
	dir    string
	ctl    string
	socket string
	recv   string

	cancel   context.CancelFunc
	done     chan error
	finished bool
	runErr   error
}

// startTestAgent starts runAgentRun in a goroutine and waits until its control
// socket exists.
func startTestAgent(t *testing.T) *testAgent {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}

	dir := t.TempDir()
	pid := strconv.Itoa(os.Getpid())
	agent := &testAgent{
		dir:    dir,
		ctl:    filepath.Join(dir, "ctl.sock"),
		socket: filepath.Join(dir, pid+".inbox.sock"),
		recv:   filepath.Join(dir, "recv.jsonl"),
		done:   make(chan error, 1),
	}
	recordPath := filepath.Join(dir, "sessions", pid+".json")

	// The MCP child is this test binary, which TestMain turns into the stub.
	t.Setenv("RSPROBE_STUB_MCP", "1")

	ctx, cancel := context.WithCancel(context.Background())
	agent.cancel = cancel
	go func() {
		agent.done <- runAgentRun(ctx, []string{
			"--config-dir", dir,
			"--name", "agent-test",
			"--cwd", dir,
			"--ctl", agent.ctl,
			"--recv-log", agent.recv,
			"--agent-peer", executable,
		})
	}()

	t.Cleanup(func() {
		agent.cancel()
		if !agent.finished {
			select {
			case agent.runErr = <-agent.done:
				agent.finished = true
			case <-time.After(agentShutdownTimeout):
				t.Error("agent run did not stop after its context was cancelled")
			}
		}
		if agent.runErr != nil {
			t.Errorf("agent run returned %v", agent.runErr)
		}
		if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
			t.Errorf("session record %s still exists after shutdown: %v", recordPath, err)
		}
		if _, err := os.Stat(agent.socket); !os.IsNotExist(err) {
			t.Errorf("inbox socket %s still exists after shutdown: %v", agent.socket, err)
		}
	})

	agent.waitReady(t)
	return agent
}

// recordPath is where the Agent writes this process's session record.
func (a *testAgent) recordPath() string {
	return filepath.Join(a.dir, "sessions", strconv.Itoa(os.Getpid())+".json")
}

// waitReady polls for the control socket the Agent creates once its MCP
// process has connected.
func (a *testAgent) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(agentReadyTimeout)
	for {
		select {
		case a.runErr = <-a.done:
			a.finished = true
			t.Fatalf("agent run exited before becoming ready: %v", a.runErr)
		default:
		}
		if _, err := os.Stat(a.ctl); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket %s did not appear", a.ctl)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (a *testAgent) readRecord(t *testing.T) agentRecord {
	t.Helper()
	record, err := readAgentRecord(a.recordPath())
	if err != nil {
		t.Fatalf("reading the session record: %v", err)
	}
	return record
}

func TestAgentRunWritesRecordAndForwardsCalls(t *testing.T) {
	// The MCP process must not inherit a parent session's identity.
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")

	agent := startTestAgent(t)

	record := agent.readRecord(t)
	if record.PID != os.Getpid() {
		t.Errorf("record pid = %d, want %d", record.PID, os.Getpid())
	}
	if record.Name != "agent-test" {
		t.Errorf("record name = %q, want %q", record.Name, "agent-test")
	}
	if record.Status != "idle" {
		t.Errorf("record status = %q, want %q", record.Status, "idle")
	}
	if record.MessagingSocketPath != agent.socket {
		t.Errorf("record socket = %q, want %q", record.MessagingSocketPath, agent.socket)
	}

	var out bytes.Buffer
	if err := runAgentCall([]string{"--ctl", agent.ctl, "echo", `{"text":"hi"}`}, &out); err != nil {
		t.Fatalf("agent call echo: %v", err)
	}
	var resp controlResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the answer %q: %v", out.String(), err)
	}
	if !resp.OK {
		t.Fatalf("echo failed: %+v", resp)
	}
	for _, want := range []string{
		"hi",
		"CLAUDE_CODE_MESSAGING_SOCKET=" + agent.socket,
		"CLAUDE_CONFIG_DIR=" + agent.dir,
	} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("answer %q does not contain %q", resp.Text, want)
		}
	}
	if strings.Contains(resp.Text, "CLAUDE_CODE_CHILD_SESSION") {
		t.Errorf("the MCP process inherited CLAUDE_CODE_CHILD_SESSION: %q", resp.Text)
	}
}

func TestAgentRecvLog(t *testing.T) {
	agent := startTestAgent(t)

	client := &ccsock.Client{NoAuth: true, Timeout: agentSendTimeout}
	if _, err := client.SendToSocket(context.Background(), agent.socket, ccsock.Message{
		Text:     "hello-body",
		Priority: ccsock.PriorityNext,
		FromName: "sender",
	}); err != nil {
		t.Fatalf("sending a frame: %v", err)
	}

	data, err := os.ReadFile(agent.recv)
	if err != nil {
		t.Fatalf("reading the receive log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("receive log = %q, want one line", string(data))
	}
	var entry recvEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decoding the receive log line %q: %v", lines[0], err)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.T); err != nil {
		t.Errorf("t = %q is not RFC3339Nano: %v", entry.T, err)
	}
	// Peer credentials are only available on Linux; elsewhere the receive log
	// records 0 and the frame is still accepted.
	if runtime.GOOS == "linux" && entry.PeerPID != os.Getpid() {
		t.Errorf("peer_pid = %d, want %d", entry.PeerPID, os.Getpid())
	}
	if entry.FromName != "sender" {
		t.Errorf("from_name = %q, want %q", entry.FromName, "sender")
	}
	if !strings.Contains(entry.Content, "hello-body") {
		t.Errorf("content = %q, want it to contain the body", entry.Content)
	}
	if !strings.Contains(entry.Content, `from-name="sender"`) {
		t.Errorf("content = %q, want the attribution envelope", entry.Content)
	}
}

// TestRecordedPeerPIDReportsUnsupported exercises the off-Linux branch on
// Linux by injecting the credential reader: the frame still gets logged with
// peer_pid 0, and the reason is printed once.
func TestRecordedPeerPIDReportsUnsupported(t *testing.T) {
	var warn bytes.Buffer
	pid := recordedPeerPID(nil, func(net.Conn) (int, error) {
		return 0, errors.ErrUnsupported
	}, &warn)
	if pid != 0 {
		t.Errorf("pid = %d, want 0", pid)
	}
	if !strings.Contains(warn.String(), peerCredentialUnsupported) {
		t.Errorf("warning = %q, want %q", warn.String(), peerCredentialUnsupported)
	}
}

func TestRecordedPeerPIDReturnsTheReadPID(t *testing.T) {
	var warn bytes.Buffer
	pid := recordedPeerPID(nil, func(net.Conn) (int, error) {
		return 42, nil
	}, &warn)
	if pid != 42 {
		t.Errorf("pid = %d, want 42", pid)
	}
	if warn.Len() != 0 {
		t.Errorf("warning = %q, want none", warn.String())
	}
}

func TestRecordedPeerPIDSwallowsOtherErrors(t *testing.T) {
	var warn bytes.Buffer
	pid := recordedPeerPID(nil, func(net.Conn) (int, error) {
		return 0, errors.New("reading credentials failed")
	}, &warn)
	if pid != 0 {
		t.Errorf("pid = %d, want 0", pid)
	}
	if warn.Len() != 0 {
		t.Errorf("warning = %q, want none", warn.String())
	}
}

func TestAgentSetRewritesInPlace(t *testing.T) {
	agent := startTestAgent(t)

	before, err := os.Stat(agent.recordPath())
	if err != nil {
		t.Fatalf("stating the session record: %v", err)
	}

	var out bytes.Buffer
	if err := runAgentSet([]string{"--ctl", agent.ctl, "--name", "x2"}, &out); err != nil {
		t.Fatalf("agent set --name: %v", err)
	}
	var resp controlResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the answer %q: %v", out.String(), err)
	}
	if !resp.OK {
		t.Fatalf("set failed: %+v", resp)
	}

	after, err := os.Stat(agent.recordPath())
	if err != nil {
		t.Fatalf("stating the rewritten record: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("the session record was replaced instead of rewritten in place")
	}
	if got := agent.readRecord(t).Name; got != "x2" {
		t.Errorf("record name = %q, want %q", got, "x2")
	}

	if err := runAgentSet([]string{"--ctl", agent.ctl, "--status", "busy"}, &out); err != nil {
		t.Fatalf("agent set --status: %v", err)
	}
	if got := agent.readRecord(t).Status; got != "busy" {
		t.Errorf("record status = %q, want %q", got, "busy")
	}
}
