package syscontainer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// logRecorder captures what pollCallWithin logs, so a test can check that a
// failed call is reported instead of failing the test silently.
type logRecorder struct {
	lines []string
}

func (r *logRecorder) logf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// TestPollCallWithinDeadlinePassedMakesNoCall checks that a wait whose deadline
// has already passed returns an error without making a tool call. The call
// function counts invocations, so a regression that calls anyway fails here.
func TestPollCallWithinDeadlinePassedMakesNoCall(t *testing.T) {
	calls := 0
	var logs logRecorder
	text, isError, err := pollCallWithin("alice", "list_agents", time.Now().Add(-time.Second), harness.ToolCallPollBound,
		func(context.Context) (*mcp.CallToolResult, error) {
			calls++
			return &mcp.CallToolResult{}, nil
		}, logs.logf)

	if err == nil {
		t.Fatal("pollCallWithin returned no error for a deadline that has already passed")
	}
	if calls != 0 {
		t.Errorf("pollCallWithin made %d calls after the deadline passed, want 0", calls)
	}
	if text != "" || isError {
		t.Errorf("pollCallWithin returned (%q, %v), want (\"\", false)", text, isError)
	}
	if !strings.Contains(err.Error(), "no time left in the wait") {
		t.Errorf("error %q does not say the wait had no time left", err)
	}
	if len(logs.lines) != 0 {
		t.Errorf("pollCallWithin logged %q for a call it never made", logs.lines)
	}
}

// TestPollCallWithinClampsToShortestBound checks that one call is bounded by
// the smaller of the poll bound and the time the wait has left. In both cases
// the call is cut at the bound, the timeout is returned as an error instead of
// failing the test, and the failure is logged with the session, the tool, the
// elapsed time and the error.
func TestPollCallWithinClampsToShortestBound(t *testing.T) {
	waitForContext := func(ctx context.Context) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cases := []struct {
		name      string
		deadline  time.Duration
		pollBound time.Duration
		wantBound time.Duration
	}{
		{
			name:      "poll bound is shorter than the wait",
			deadline:  time.Minute,
			pollBound: 50 * time.Millisecond,
			wantBound: 50 * time.Millisecond,
		},
		{
			name:      "the wait is shorter than the poll bound",
			deadline:  100 * time.Millisecond,
			pollBound: harness.ToolCallPollBound,
			wantBound: 100 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs logRecorder
			start := time.Now()
			text, isError, err := pollCallWithin("alice", "list_agents", time.Now().Add(tc.deadline), tc.pollBound, waitForContext, logs.logf)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("pollCallWithin returned no error for a call that timed out")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error %v does not wrap context.DeadlineExceeded", err)
			}
			if text != "" || isError {
				t.Errorf("pollCallWithin returned (%q, %v), want (\"\", false)", text, isError)
			}
			if elapsed > tc.wantBound+time.Second {
				t.Errorf("call took %s, want about the %s bound", elapsed, tc.wantBound)
			}
			if len(logs.lines) != 1 {
				t.Fatalf("pollCallWithin logged %d lines, want 1: %q", len(logs.lines), logs.lines)
			}
			line := logs.lines[0]
			for _, want := range []string{"alice", "list_agents", "failed after", "context deadline exceeded"} {
				if !strings.Contains(line, want) {
					t.Errorf("log line %q does not contain %q", line, want)
				}
			}
		})
	}
}

// TestPollCallWithinReturnsCallShape checks that a successful call returns the
// same shape as Call: the text contents joined with a newline and the result's
// IsError flag, with no log.
func TestPollCallWithinReturnsCallShape(t *testing.T) {
	var logs logRecorder
	text, isError, err := pollCallWithin("alice", "list_agents", time.Now().Add(time.Minute), harness.ToolCallPollBound,
		func(ctx context.Context) (*mcp.CallToolResult, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("the call context has no deadline")
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "first"},
					&mcp.TextContent{Text: "second"},
				},
				IsError: true,
			}, nil
		}, logs.logf)

	if err != nil {
		t.Fatalf("pollCallWithin returned error %v for a successful call", err)
	}
	if text != "first\nsecond" {
		t.Errorf("text = %q, want %q", text, "first\nsecond")
	}
	if !isError {
		t.Error("isError = false, want the result's IsError flag")
	}
	if len(logs.lines) != 0 {
		t.Errorf("pollCallWithin logged %q for a successful call", logs.lines)
	}
}
