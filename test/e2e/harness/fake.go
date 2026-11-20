package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/testkit/fakesession"
	"github.com/wangzw/agent-peer/test/e2e/ctl"
)

// fakeAnswerSlack is added to a frame wait's own timeout for the control
// answer, so the node agent's timeout fires before the client's.
const fakeAnswerSlack = 5 * time.Second

// FakeSession is the simulated session surface a test drives. A local session
// returns the real *fakesession.Fake; a session in a container returns a proxy
// that forwards each operation to the node agent over the control protocol.
// The methods the control protocol cannot express fail the test with an
// unsupported-operation message.
type FakeSession interface {
	ConfigDir() string
	PID() int
	RecordPath() string
	SocketPath() string
	SetName(name string)
	Rename(name string)
	SetStatus(status string)
	SetBusy(busy bool)
	SetSessionID(sessionID string)
	RemoveRecord()
	RestoreRecord()
	WritePartialRecord(prefix string)
	SetRecordModTime(mod time.Time)
	SetMode(mode fakesession.Mode)
	SetBusyDelay(delay time.Duration)
	Release()
	Frames() []fakesession.Frame
	WaitFrame(timeout time.Duration) (fakesession.Frame, bool)
	WaitFrameN(n int, timeout time.Duration) (fakesession.Frame, bool)
	WaitNoConnections(timeout time.Duration) bool
	TakeErrors() []error
	InjectBackgroundError(err error)
	Close()
	RemoveSocket()
}

// A local session returns the real fake, so the interface must accept it.
var _ FakeSession = (*fakesession.Fake)(nil)

// remoteFake is the container session's proxy. Every operation travels over
// the control protocol to the node agent holding the fakesession.Fake, and a
// control failure fails the test the way a local fake would.
type remoteFake struct {
	tb     testing.TB
	name   string
	client *ctl.Client
	info   ctl.SessionInfo

	mu     sync.Mutex
	cursor int
}

// ConfigDir returns the session's configuration directory.
func (f *remoteFake) ConfigDir() string { return f.info.ConfigDir }

// PID returns the fake session's process ID.
func (f *remoteFake) PID() int { return f.info.FakePID }

// RecordPath returns the path of the registry record.
func (f *remoteFake) RecordPath() string { return f.info.RecordPath }

// SocketPath returns the inbox socket path.
func (f *remoteFake) SocketPath() string { return f.info.SocketPath }

// SetName changes the session name in the record.
func (f *remoteFake) SetName(name string) { f.apply("rename", name) }

// Rename is an alias for SetName.
func (f *remoteFake) Rename(name string) { f.SetName(name) }

// SetStatus changes the status field in the record.
func (f *remoteFake) SetStatus(status string) { f.apply("set_status", status) }

// SetBusy writes "busy" or "idle" to the record.
func (f *remoteFake) SetBusy(busy bool) { f.apply("set_busy", busy) }

// SetSessionID changes the session identifier in the record.
func (f *remoteFake) SetSessionID(sessionID string) { f.apply("set_session_id", sessionID) }

// RemoveRecord deletes the registry record but leaves the socket bound.
func (f *remoteFake) RemoveRecord() { f.apply("remove_record", nil) }

// RestoreRecord writes the record again from the current fields.
func (f *remoteFake) RestoreRecord() { f.apply("restore_record", nil) }

// RemoveSocket stops accepting connections and removes the bound socket while
// leaving the registry record in place.
func (f *remoteFake) RemoveSocket() { f.apply("remove_socket", nil) }

// SetMode changes how subsequent connections are handled.
func (f *remoteFake) SetMode(mode fakesession.Mode) { f.apply("set_mode", modeName(mode)) }

// SetBusyDelay changes how long each accepted connection waits before its
// frames are read.
func (f *remoteFake) SetBusyDelay(delay time.Duration) {
	f.apply("set_busy_delay", delay.Milliseconds())
}

// Release ends one hold created by HoldOpen.
func (f *remoteFake) Release() { f.apply("release", nil) }

// TakeErrors returns the background failures the fake recorded since the
// previous call and forgets them.
func (f *remoteFake) TakeErrors() []error {
	f.tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	res, err := f.client.Errors(ctx, ctl.SessionIDRequest{ID: f.info.ID})
	if err != nil {
		f.tb.Fatalf("harness: session %s: reading simulated session failures: %v", f.name, err)
		return nil
	}
	errs := make([]error, 0, len(res.Errors))
	for _, message := range res.Errors {
		errs = append(errs, errors.New(message))
	}
	return errs
}

// InjectBackgroundError records err in the fake as a background failure. It
// exists for tests of the reporting path.
func (f *remoteFake) InjectBackgroundError(err error) {
	if err == nil {
		return
	}
	f.apply("inject_background_error", err.Error())
}

// WritePartialRecord is not expressible over the control protocol.
func (f *remoteFake) WritePartialRecord(string) { f.unsupported("WritePartialRecord") }

// SetRecordModTime is not expressible over the control protocol.
func (f *remoteFake) SetRecordModTime(time.Time) { f.unsupported("SetRecordModTime") }

// Close is not expressible over the control protocol; use Session.End.
func (f *remoteFake) Close() { f.unsupported("Close") }

// Frames returns the message frames received so far, in arrival order.
func (f *remoteFake) Frames() []fakesession.Frame {
	f.tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	res, err := f.client.Frames(ctx, ctl.SessionFramesRequest{ID: f.info.ID})
	if err != nil {
		f.tb.Fatalf("harness: session %s: reading frames: %v", f.name, err)
		return nil
	}
	return remoteFrames(res.Frames)
}

// WaitFrame waits for the next frame not yet returned by WaitFrame or
// WaitFrameN, walking the frames in arrival order as the local fake does.
func (f *remoteFake) WaitFrame(timeout time.Duration) (fakesession.Frame, bool) {
	f.tb.Helper()
	f.mu.Lock()
	n := f.cursor + 1
	f.mu.Unlock()
	return f.waitFrameN(n, timeout)
}

// WaitFrameN waits for the nth frame and marks every frame up to n as
// returned.
func (f *remoteFake) WaitFrameN(n int, timeout time.Duration) (fakesession.Frame, bool) {
	f.tb.Helper()
	if n < 1 {
		f.tb.Fatalf("harness: session %s: WaitFrameN index %d, want at least 1", f.name, n)
		return fakesession.Frame{}, false
	}
	return f.waitFrameN(n, timeout)
}

// waitFrameN waits until the node agent has recorded n frames and returns the
// nth. It reports false when the frame did not arrive in time.
func (f *remoteFake) waitFrameN(n int, timeout time.Duration) (fakesession.Frame, bool) {
	f.tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+fakeAnswerSlack)
	defer cancel()
	res, err := f.client.Frames(ctx, ctl.SessionFramesRequest{
		ID:        f.info.ID,
		WaitN:     n,
		TimeoutMS: timeout.Milliseconds(),
	})
	if err != nil {
		f.tb.Fatalf("harness: session %s: waiting for a frame: %v", f.name, err)
		return fakesession.Frame{}, false
	}
	if !res.OK || len(res.Frames) < n {
		return fakesession.Frame{}, false
	}
	f.mu.Lock()
	if f.cursor < n {
		f.cursor = n
	}
	f.mu.Unlock()
	return remoteFrame(res.Frames[n-1]), true
}

// WaitNoConnections waits until the fake in the container tracks no inbox
// connection. The node agent's fake wakes the wait when a handler leaves its
// connection, so the answer does not depend on polling.
func (f *remoteFake) WaitNoConnections(timeout time.Duration) bool {
	f.tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+fakeAnswerSlack)
	defer cancel()
	res, err := f.client.WaitNoConnections(ctx, ctl.SessionWaitNoConnectionsRequest{
		ID:        f.info.ID,
		TimeoutMS: timeout.Milliseconds(),
	})
	if err != nil {
		f.tb.Fatalf("harness: session %s: waiting for no inbox connections: %v", f.name, err)
		return false
	}
	return res.OK
}

// apply forwards one operation and fails the test when the node agent rejects
// it.
func (f *remoteFake) apply(op string, arg any) {
	f.tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitBound)
	defer cancel()
	if err := f.client.Fake(ctx, ctl.SessionFakeRequest{ID: f.info.ID, Op: op, Arg: arg}); err != nil {
		f.tb.Fatalf("harness: session %s: Fake operation %s: %v", f.name, op, err)
	}
}

// unsupported fails the test for an operation the control protocol cannot
// carry.
func (f *remoteFake) unsupported(method string) {
	f.tb.Helper()
	f.tb.Fatalf("harness: session %s: Fake.%s is not supported for a session in a container", f.name, method)
}

// remoteFrames converts frames from the control protocol.
func remoteFrames(frames []ctl.SessionFrame) []fakesession.Frame {
	out := make([]fakesession.Frame, 0, len(frames))
	for _, frame := range frames {
		out = append(out, remoteFrame(frame))
	}
	return out
}

// remoteFrame converts one frame, keeping the local fake's invariants: AuthRaw
// is nil unless an authentication line preceded the frame, and Fields is
// always non-nil.
func remoteFrame(frame ctl.SessionFrame) fakesession.Frame {
	var authRaw []byte
	if frame.Auth && frame.AuthRaw != "" {
		authRaw = []byte(frame.AuthRaw)
	}
	fields := make(map[string]any, len(frame.Fields))
	for key, value := range frame.Fields {
		fields[key] = value
	}
	return fakesession.Frame{
		Raw:       []byte(frame.Raw),
		Auth:      frame.Auth,
		AuthRaw:   authRaw,
		MsgID:     frame.MsgID,
		Type:      frame.Type,
		FromName:  frame.FromName,
		From:      frame.From,
		SessionID: frame.SessionID,
		Priority:  frame.Priority,
		Content:   frame.Content,
		Fields:    fields,
	}
}
