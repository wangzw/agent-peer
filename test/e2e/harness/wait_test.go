package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
	"github.com/wangzw/agent-peer/test/e2e/node"
)

// recordTB captures Fatalf calls so a test can inspect the failure message
// without ending the process. Fatalf panics with fatalStop to unwind the code
// under test the way the real testing.T would.
type recordTB struct {
	testing.TB // always nil; embedded for the unexported method only

	mu     sync.Mutex
	fatals []string
}

type fatalStop struct{}

func (r *recordTB) Helper() {}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	panic(fatalStop{})
}

func (r *recordTB) lastFatal() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fatals) == 0 {
		return ""
	}
	return r.fatals[len(r.fatals)-1]
}

// captureFatal runs fn and returns the message of the Fatalf it raised. A
// different panic is re-raised so a genuine bug is not swallowed.
func captureFatal(t *testing.T, tb *recordTB, fn func()) string {
	t.Helper()
	var msg string
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if _, ok := r.(fatalStop); !ok {
				panic(r)
			}
			msg = tb.lastFatal()
		}()
		fn()
	}()
	return msg
}

func TestEventuallyReportsLastState(t *testing.T) {
	tb := &recordTB{}
	bound := 200 * time.Millisecond

	calls := 0
	start := time.Now()
	msg := captureFatal(t, tb, func() {
		Eventually(tb, bound, "example condition", func() (bool, string) {
			calls++
			return false, fmt.Sprintf("state-%d", calls)
		})
	})
	elapsed := time.Since(start)

	if msg == "" {
		t.Fatal("Eventually returned without reporting a failure")
	}
	last := fmt.Sprintf("state-%d", calls)
	if !strings.Contains(msg, last) {
		t.Fatalf("failure message %q does not contain the last state %q", msg, last)
	}
	if elapsed < bound {
		t.Fatalf("Eventually returned after %s, want at least the bound %s", elapsed, bound)
	}
}

func TestConsistentlyFailsOnFirstViolation(t *testing.T) {
	tb := &recordTB{}
	window := 2 * time.Second

	calls := 0
	start := time.Now()
	msg := captureFatal(t, tb, func() {
		Consistently(tb, window, "steady condition", func() (bool, string) {
			calls++
			return calls != 3, fmt.Sprintf("state-%d", calls)
		})
	})
	elapsed := time.Since(start)

	if msg == "" {
		t.Fatal("Consistently returned without reporting a failure")
	}
	if calls != 3 {
		t.Fatalf("condition ran %d times, want it to stop at the third violation", calls)
	}
	if !strings.Contains(msg, "state-3") {
		t.Fatalf("failure message %q does not contain the state at the violation", msg)
	}
	if elapsed >= window {
		t.Fatalf("Consistently returned after %s, want less than the window %s", elapsed, window)
	}
}

func TestConfigYAMLMerge(t *testing.T) {
	merged := ConfigYAML(FastTimers, "send:\n  request_timeout: 10s\n")

	var cfg struct {
		Link struct {
			ReconnectMin string `yaml:"reconnect_min"`
			ReconnectMax string `yaml:"reconnect_max"`
			DialTimeout  string `yaml:"dial_timeout"`
		} `yaml:"link"`
		Send struct {
			LinkBackoff    string `yaml:"link_backoff"`
			RequestTimeout string `yaml:"request_timeout"`
		} `yaml:"send"`
		Broker struct {
			AddressCheckInterval string `yaml:"address_check_interval"`
		} `yaml:"broker"`
	}
	if err := yaml.Unmarshal([]byte(merged), &cfg); err != nil {
		t.Fatalf("merged configuration is not valid YAML: %v\n%s", err, merged)
	}

	if cfg.Send.RequestTimeout != "10s" {
		t.Fatalf("send.request_timeout = %q, want the later fragment to win with %q", cfg.Send.RequestTimeout, "10s")
	}
	if cfg.Link.ReconnectMin != "100ms" {
		t.Fatalf("link.reconnect_min = %q, want %q", cfg.Link.ReconnectMin, "100ms")
	}
	if cfg.Link.ReconnectMax != "400ms" {
		t.Fatalf("link.reconnect_max = %q, want %q", cfg.Link.ReconnectMax, "400ms")
	}
	if cfg.Link.DialTimeout != "500ms" {
		t.Fatalf("link.dial_timeout = %q, want %q", cfg.Link.DialTimeout, "500ms")
	}
	if cfg.Send.LinkBackoff != "100ms" {
		t.Fatalf("send.link_backoff = %q, want %q", cfg.Send.LinkBackoff, "100ms")
	}
	if cfg.Broker.AddressCheckInterval != "500ms" {
		t.Fatalf("broker.address_check_interval = %q, want %q", cfg.Broker.AddressCheckInterval, "500ms")
	}

	if merged != ConfigYAML(FastTimers, "send:\n  request_timeout: 10s\n") {
		t.Fatalf("ConfigYAML is not deterministic:\n%s", merged)
	}
}

// startupBackend answers the first startingCalls tool calls with the error the
// MCP server returns while its background connection loop is still in
// PhaseStarting, then answers successfully. The text is the product's
// textStarting from internal/mcpserver/errtext.go.
type startupBackend struct {
	startingCalls int
	calls         []string
}

func (b *startupBackend) call(t testing.TB, tool string, args map[string]any) Result {
	t.Helper()
	b.calls = append(b.calls, tool)
	if b.startingCalls > 0 {
		b.startingCalls--
		return Result{ToolResult: node.ToolResult{
			IsError: true,
			Text:    fmt.Sprintf("%s failed: the connection to the Broker is still being set up; retry shortly (code: internal)", tool),
		}}
	}
	return Result{}
}

func (b *startupBackend) callErr(tool string, args map[string]any) (Result, error) {
	return Result{}, nil
}

func (b *startupBackend) tools(t testing.TB) []node.ToolInfo { return nil }

func (b *startupBackend) stopMCP(t testing.TB, sig syscall.Signal) int { return 0 }

func (b *startupBackend) restartMCP(t testing.TB) {}

func (b *startupBackend) startExtraMCP(t testing.TB) {}

func (b *startupBackend) inboxConnections(t testing.TB) int { return 0 }

func (b *startupBackend) fake() FakeSession { return nil }

func (b *startupBackend) takeFakeErrors(t testing.TB) []error { return nil }

func (b *startupBackend) end() error { return nil }

// TestWaitConnectedWaitsOutStarting reproduces the startup race on an operator
// session: the first management call runs while the MCP server is still in
// PhaseStarting and fails. WaitConnected must keep polling an identity-free
// tool until the connection exists, after which the management call succeeds.
func TestWaitConnectedWaitsOutStarting(t *testing.T) {
	backend := &startupBackend{startingCalls: 3}
	session := &Session{name: "operator", impl: backend}

	first := session.Call(t, "create_pairing_key", nil)
	if !first.IsError || !strings.Contains(first.Text, "still being set up") {
		t.Fatalf("the first management call did not reproduce the starting race: %+v", first)
	}

	WaitConnected(t, session, time.Second)

	for _, tool := range backend.calls[1:] {
		if tool != "list_brokers" {
			t.Fatalf("WaitConnected called %s, want only the identity-free list_brokers", tool)
		}
	}

	if got := session.Call(t, "create_pairing_key", nil); got.IsError {
		t.Fatalf("create_pairing_key still failed after WaitConnected: %s", got.Text)
	}
}

// TestWaitConnectedAgainstStartingMCPServer reproduces the race deterministically
// with the real MCP server process. The fake Broker's Hello blocks, which keeps
// the server in PhaseStarting: a management call made then fails with the
// product's starting text. WaitConnected must wait until Hello completes before
// the next management call.
func TestWaitConnectedAgainstStartingMCPServer(t *testing.T) {
	release := make(chan struct{})
	socket := fakelocal.SocketPath(t)
	fakelocal.Start(t, socket, fakelocal.Handlers{
		Hello: func(ctx context.Context, req *localpb.HelloRequest) (*localpb.HelloResponse, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &localpb.HelloResponse{ProtocolVersion: localpb.ProtocolVersion}, nil
		},
		ListBrokers: func(context.Context, *localpb.ListBrokersRequest) (*localpb.ListBrokersResponse, error) {
			return &localpb.ListBrokersResponse{}, nil
		},
		CreatePairingKey: func(context.Context, *localpb.CreatePairingKeyRequest) (*localpb.CreatePairingKeyResponse, error) {
			return &localpb.CreatePairingKeyResponse{Key: "test-key"}, nil
		},
	})

	host := NewLocalHost(t, LocalOptions{Name: "starting"})
	session := host.StartSession(t, SessionSpec{
		Name:        "operator",
		StartMCP:    true,
		NoSocketEnv: true,
		Env:         map[string]string{"AGENT_PEER_SOCKET": socket},
	})
	defer session.End(t)

	first := session.Call(t, "create_pairing_key", nil)
	if !first.IsError || !strings.Contains(first.Text, "still being set up") {
		t.Fatalf("the first management call did not reproduce the starting race: %+v", first)
	}

	close(release)
	WaitConnected(t, session, 3*time.Second)

	if got := session.Call(t, "create_pairing_key", nil); got.IsError {
		t.Fatalf("create_pairing_key still failed after WaitConnected: %s", got.Text)
	}
}
