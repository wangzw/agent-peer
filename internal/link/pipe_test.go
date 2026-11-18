package link

import (
	"errors"
	"sync"
	"testing"

	"github.com/wangzw/agent-peer/internal/link/linkpb"
)

// errPipeClosed reports a frame stream whose both ends have been closed.
var errPipeClosed = errors.New("link: pipe closed")

// pipeBuffer is how many frames one direction of a pipe buffers before Send
// blocks.
const pipeBuffer = 64

// pipeEnd is one end of an in-memory frame stream. It records the frames it
// sent, can suspend Send, and signals when a goroutine is blocked in Recv.
type pipeEnd struct {
	in  chan *linkpb.Frame
	out chan *linkpb.Frame

	mu         sync.Mutex
	sentFrames []*linkpb.Frame

	// blockSend, when non-nil, makes Send wait until the channel is closed.
	// Tests set it before the goroutine that sends starts.
	blockSend chan struct{}

	closed    chan struct{}
	closeOnce *sync.Once

	recvWait chan struct{}
	recvOnce *sync.Once
}

// newPipe returns the two ends of an in-memory bidirectional stream.
func newPipe(t testing.TB) (a, b *pipeEnd) {
	t.Helper()
	ab := make(chan *linkpb.Frame, pipeBuffer)
	ba := make(chan *linkpb.Frame, pipeBuffer)
	closed := make(chan struct{})
	closeOnce := new(sync.Once)
	a = &pipeEnd{
		in: ba, out: ab,
		closed: closed, closeOnce: closeOnce,
		recvWait: make(chan struct{}), recvOnce: new(sync.Once),
	}
	b = &pipeEnd{
		in: ab, out: ba,
		closed: closed, closeOnce: closeOnce,
		recvWait: make(chan struct{}), recvOnce: new(sync.Once),
	}
	t.Cleanup(a.closeBoth)
	return a, b
}

// Send records f and hands it to the peer. It blocks while blockSend is set and
// not closed, and while the peer's buffer is full.
func (p *pipeEnd) Send(f *linkpb.Frame) error {
	if p.blockSend != nil {
		select {
		case <-p.blockSend:
		case <-p.closed:
			return errPipeClosed
		}
	}
	select {
	case <-p.closed:
		return errPipeClosed
	default:
	}
	p.mu.Lock()
	p.sentFrames = append(p.sentFrames, cloneFrame(f))
	p.mu.Unlock()
	select {
	case p.out <- f:
		return nil
	case <-p.closed:
		return errPipeClosed
	}
}

// Recv returns the next frame from the peer. It closes recvWaiting once a
// goroutine has to wait for one.
func (p *pipeEnd) Recv() (*linkpb.Frame, error) {
	select {
	case f := <-p.in:
		return f, nil
	default:
	}
	p.recvOnce.Do(func() { close(p.recvWait) })
	select {
	case f := <-p.in:
		return f, nil
	case <-p.closed:
		return nil, errPipeClosed
	}
}

// closeBoth closes both ends. It is safe to call more than once.
func (p *pipeEnd) closeBoth() {
	p.closeOnce.Do(func() { close(p.closed) })
}

// sent returns a copy of the frames this end sent, in order.
func (p *pipeEnd) sent() []*linkpb.Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*linkpb.Frame, len(p.sentFrames))
	copy(out, p.sentFrames)
	return out
}

// recvWaiting is closed once a goroutine blocked in Recv on this end.
func (p *pipeEnd) recvWaiting() <-chan struct{} {
	return p.recvWait
}

// cloneFrame copies a frame so a test can inspect it after it was handed to
// the stream.
func cloneFrame(f *linkpb.Frame) *linkpb.Frame {
	if f == nil {
		return nil
	}
	return &linkpb.Frame{
		Kind:      f.GetKind(),
		RequestId: f.GetRequestId(),
		IsReply:   f.GetIsReply(),
		Payload:   append([]byte(nil), f.GetPayload()...),
	}
}
