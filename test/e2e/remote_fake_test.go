//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// containerFakeBound is the end-to-end wait bound for the fake proxy. No
// product timer participates in these waits, so the plan's sum-of-timers-plus-
// three-seconds convention reduces to the three-second test wait.
const containerFakeBound = 3 * time.Second

// containerInboxFrame is the frame the tests write into a container session's
// inbox socket.
const containerInboxFrame = `{"msg_id":"m1","type":"message","message":{"content":"held"}}`

// TestContainerSessionFakeProxy drives a container session's fakesession.Fake
// from the host through the node control protocol. The session and its frames
// live in the container process, so every operation travels over
// /v1/session/fake and the frames come back over /v1/session/frames.
func TestContainerSessionFakeProxy(t *testing.T) {
	c := harness.Up(t, harness.TopoSingle)
	node := c.Node("hostA")

	t.Run("release unblocks a held frame", func(t *testing.T) {
		session := node.StartSession(t, harness.SessionSpec{Name: "held", Mode: fakesession.HoldOpen})
		defer session.End(t)

		writeContainerInboxFrame(t, node, session.Fake().SocketPath(), containerInboxFrame)

		// Wait until the fake took the connection and is holding it, so the
		// Release below has something to release.
		harness.Eventually(t, containerFakeBound, "the inbox connection is held open", func() (bool, string) {
			n := session.InboxConnections(t)
			return n == 1, fmt.Sprintf("InboxConnections() = %d, want 1", n)
		})

		session.Fake().Release()

		frame := session.WaitFrame(t, containerFakeBound)
		if frame.MsgID != "m1" || frame.Content != "held" {
			t.Fatalf("frame = {MsgID: %q, Content: %q}, want m1/held", frame.MsgID, frame.Content)
		}

		// The handler leaves the hold only when Release reaches the container;
		// the connection therefore counts zero only after the release.
		harness.Eventually(t, containerFakeBound, "the released connection closes", func() (bool, string) {
			n := session.InboxConnections(t)
			return n == 0, fmt.Sprintf("InboxConnections() = %d, want 0", n)
		})
	})

	t.Run("set mode applies to the next connection", func(t *testing.T) {
		session := node.StartSession(t, harness.SessionSpec{Name: "mode", Mode: fakesession.CloseImmediately})
		defer session.End(t)

		// CloseImmediately discards the connection without recording a frame.
		// After this change the next connection must be held and recorded.
		session.Fake().SetMode(fakesession.HoldOpen)
		writeContainerInboxFrame(t, node, session.Fake().SocketPath(), containerInboxFrame)

		frame := session.WaitFrame(t, containerFakeBound)
		if frame.MsgID != "m1" || frame.Content != "held" {
			t.Fatalf("frame = {MsgID: %q, Content: %q}, want m1/held", frame.MsgID, frame.Content)
		}
		session.Fake().Release()
	})
}

// writeContainerInboxFrame writes one frame line to a session's inbox socket
// from inside the container. netcat waits for the receiver to close, so the
// shell backgrounds it and returns at once; -N makes it half-close its side
// when the frame is written, which is what lets a released HoldOpen handler
// drain to EOF and finish.
func writeContainerInboxFrame(t *testing.T, node *harness.Node, socket, frame string) {
	t.Helper()
	script := "printf '%s\\n' " + shellQuote(frame) + " | nc -N -U " + shellQuote(socket) + " >/dev/null 2>&1 &"
	res := node.Exec(t, []string{"sh", "-c", script}, nil)
	if res.ExitCode != 0 {
		t.Fatalf("writing a frame to %s: exit code %d: %s", socket, res.ExitCode, res.Stderr)
	}
}

// shellQuote quotes value for sh. The frame and socket contain no single
// quotes, but quoting handles them anyway.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
