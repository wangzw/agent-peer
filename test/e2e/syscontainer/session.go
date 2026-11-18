//go:build e2e

package syscontainer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
)

// mcpHandshakeBound is the upper bound for the MCP server handshake.
const mcpHandshakeBound = 30 * time.Second

// toolCallBound bounds one tool call.
const toolCallBound = 15 * time.Second

// Session is one simulated Claude Code session running as an account.
type Session struct {
	// Fake is the simulated session record and inbox socket.
	Fake *fakesession.Fake
	// Account is the account the MCP server runs as.
	Account string
	// MCP is the connected MCP client session.
	MCP *mcp.ClientSession
}

// sessionConfig collects the options of NewSession.
type sessionConfig struct {
	env    []string
	socket string
}

// SessionOption changes how NewSession builds the session.
type SessionOption func(*sessionConfig)

// WithEnv adds environment entries ("NAME=value") to the MCP server process.
func WithEnv(kv ...string) SessionOption {
	return func(c *sessionConfig) {
		c.env = append(c.env, kv...)
	}
}

// WithSocket makes the session record name an inbox socket the test binds
// itself. The fake does not bind it.
func WithSocket(path string) SessionOption {
	return func(c *sessionConfig) {
		c.socket = path
	}
}

// NewSession creates a fake session for account under
// /home/<account>/.claude, hands the configuration directory, registry record
// and socket to the account, then starts "agent-peer mcp" as the account with
// CLAUDE_CODE_MESSAGING_SOCKET and CLAUDE_CONFIG_DIR set and connects an MCP
// client over its standard streams.
func NewSession(t *testing.T, account, name string, opts ...SessionOption) *Session {
	t.Helper()
	if !InContainer() {
		t.Fatalf("syscontainer: NewSession runs inside the container")
	}

	var cfg sessionConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	u, uid, gid := accountIDs(t, account)
	configDir := filepath.Join(u.HomeDir, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("syscontainer: creating %s: %v", configDir, err)
	}
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("syscontainer: creating %s: %v", sessionsDir, err)
	}

	socket := cfg.socket
	if socket == "" {
		socket = fmt.Sprintf("/tmp/ap-%s-%s.sock", account, name)
	}

	fake := fakesession.New(t, fakesession.Options{
		ConfigDir:  configDir,
		Name:       name,
		SocketPath: socket,
		NoBind:     cfg.socket != "",
	})

	for _, path := range []string{configDir, sessionsDir, fake.RecordPath()} {
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			t.Fatalf("syscontainer: chown %s: %v", path, err)
		}
	}
	if cfg.socket == "" {
		if err := os.Chown(fake.SocketPath(), int(uid), int(gid)); err != nil {
			t.Fatalf("syscontainer: chown %s: %v", fake.SocketPath(), err)
		}
	}

	env := []string{
		"CLAUDE_CODE_MESSAGING_SOCKET=" + socket,
		"CLAUDE_CONFIG_DIR=" + configDir,
	}
	env = append(env, cfg.env...)
	cmd := Command(account, env, agentPeerBinary, "mcp")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-syscontainer", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), mcpHandshakeBound)
	defer cancel()
	mcpSession, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("syscontainer: connecting to the MCP server as %s: %v\n%s",
			account, err, strings.TrimSpace(stderr.String()))
	}

	session := &Session{Fake: fake, Account: account, MCP: mcpSession}
	t.Cleanup(func() {
		if err := mcpSession.Close(); err != nil && t.Failed() {
			t.Logf("syscontainer: closing the MCP server as %s: %v", account, err)
		}
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("syscontainer: MCP server stderr as %s:\n%s", account, strings.TrimSpace(stderr.String()))
		}
	})
	return session
}

// Call invokes a tool. isError reports the result's IsError flag; the returned
// text joins every text content with a newline. A protocol failure fails the
// test.
func (s *Session) Call(t *testing.T, tool string, args map[string]any) (text string, isError bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), toolCallBound)
	defer cancel()
	result, err := s.MCP.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("syscontainer: calling %s as %s: %v", tool, s.Account, err)
	}
	var texts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n"), result.IsError
}
