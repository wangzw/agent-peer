//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// pollDecode calls tool from inside a wait and decodes its structured result
// into T. A transport or tool failure is returned as the state string the wait
// callback reports, so the wait can poll again. join_topic and leave_topic are
// polled with harness.TopicCallPollBound because their handler may spend
// topicRPCTimeout before the update hint runs; every other tool, list_topics
// included, uses the default harness.ToolCallPollBound.
func pollDecode[T any](t *testing.T, deadline time.Time, s *harness.Session, tool string, args map[string]any) (T, string) {
	t.Helper()
	var zero T
	pollBound := harness.ToolCallPollBound
	if brokerTopicTool(tool) {
		pollBound = harness.TopicCallPollBound
	}
	r, err := s.PollCallWithin(t, deadline, pollBound, tool, args)
	if err != nil {
		return zero, err.Error()
	}
	if r.IsError {
		return zero, r.Text
	}
	out, err := harness.Decode[T](string(r.Structured))
	if err != nil {
		return zero, "decoding " + tool + ": " + err.Error()
	}
	return out, ""
}
