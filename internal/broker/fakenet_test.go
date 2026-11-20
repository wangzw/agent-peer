package broker

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/wangzw/agent-peer/internal/link"
)

// linkPair is an ordered pair of peers. The first element is the sender or the
// caller, the second the receiver or the peer acted on.
type linkPair [2]peerID

// pairKey returns a canonical key for an unordered pair, so both ends of a link
// read and rotate the same channel.
func pairKey(a, b peerID) linkPair {
	if lessPeer(a, b) {
		return linkPair{a, b}
	}
	return linkPair{b, a}
}

func lessPeer(a, b peerID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// fakeNet is an in-process network of fake links between delivery instances. It
// connects several delivery values in one process and lets a test force link
// drops, lost replies and requests that were never sent.
type fakeNet struct {
	mu sync.Mutex

	// ends holds one fakeEnd per Broker; an end is created on first use.
	ends map[peerID]*fakeEnd
	// up records whether an unordered pair is linked.
	up map[linkPair]bool
	// down holds the channel closed when an unordered pair goes down. Both
	// ends watch the same channel.
	down map[linkPair]chan struct{}
	// reqs records the frames sent in one direction, in order.
	reqs map[linkPair][]link.Frame
	// kickCounts counts Kick calls in one direction.
	kickCounts map[linkPair]int
	// kickHooks holds the hooks run on Kick in one direction.
	kickHooks map[linkPair][]func()
	// drops counts the replies still to be lost in one direction.
	drops map[linkPair]int
	// replyHook, when set, runs when a reply reaches the caller, before the
	// request resolves it. A test uses it to move the link at that instant.
	replyHook func()
	// handlerCancels holds the cancel function of every handler still running,
	// so stop can end one a test abandoned; closed refuses later handlers.
	handlerCancels map[int]context.CancelFunc
	nextHandler    int
	closed         bool
}

// newFakeNet returns an empty network. All links start down.
func newFakeNet(t *testing.T) *fakeNet {
	t.Helper()
	n := &fakeNet{
		ends:           make(map[peerID]*fakeEnd),
		up:             make(map[linkPair]bool),
		down:           make(map[linkPair]chan struct{}),
		reqs:           make(map[linkPair][]link.Frame),
		kickCounts:     make(map[linkPair]int),
		kickHooks:      make(map[linkPair][]func()),
		drops:          make(map[linkPair]int),
		handlerCancels: make(map[int]context.CancelFunc),
	}
	t.Cleanup(n.stop)
	return n
}

// stop cancels every handler still running and refuses later ones, so a handler
// a test abandoned when its request ended does not outlive the test. A handler
// context outlives the request by design, so only cleanup can end it.
func (n *fakeNet) stop() {
	n.mu.Lock()
	n.closed = true
	cancels := make([]context.CancelFunc, 0, len(n.handlerCancels))
	for _, cancel := range n.handlerCancels {
		cancels = append(cancels, cancel)
	}
	n.handlerCancels = make(map[int]context.CancelFunc)
	n.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// watchHandler registers a handler's cancel function for stop and returns the
// id unwatchHandler takes. A handler registered after stop is canceled at once.
func (n *fakeNet) watchHandler(cancel context.CancelFunc) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		cancel()
		return 0
	}
	n.nextHandler++
	n.handlerCancels[n.nextHandler] = cancel
	return n.nextHandler
}

func (n *fakeNet) unwatchHandler(id int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.handlerCancels, id)
}

// end returns the fakeEnd of self, creating it on first use.
func (n *fakeNet) end(self peerID) *fakeEnd {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.endLocked(self)
}

func (n *fakeNet) endLocked(self peerID) *fakeEnd {
	if e := n.ends[self]; e != nil {
		return e
	}
	e := &fakeEnd{
		net:      n,
		self:     self,
		handlers: make(map[link.FrameKind]func(context.Context, peerID, link.Frame) link.Frame),
	}
	n.ends[self] = e
	return e
}

// setUp marks the link between a and b up or down and fires the matching
// callbacks on both ends, a first, before it returns. Callbacks fire only when
// the state changes, like the real link's one event per transition; repeating
// the state that already holds does nothing. Callbacks run in order and outside
// the network lock so they may call back into it.
func (n *fakeNet) setUp(a, b peerID, up bool) {
	n.mu.Lock()

	key := pairKey(a, b)
	wasUp := n.up[key]
	changed := false
	var closing chan struct{}
	switch {
	case up && !wasUp:
		changed = true
		n.up[key] = true
		n.down[key] = make(chan struct{})
	case !up && wasUp:
		changed = true
		delete(n.up, key)
		closing = n.down[key]
		delete(n.down, key)
	}

	endA := n.endLocked(a)
	endB := n.endLocked(b)
	var upsA, upsB []func(peerID, []byte)
	var downsA, downsB []func(peerID)
	if changed {
		upsA, downsA = endA.callbacksLocked()
		upsB, downsB = endB.callbacksLocked()
	}
	n.mu.Unlock()

	if !changed {
		return
	}
	if closing != nil {
		close(closing)
	}
	if up {
		for _, f := range upsA {
			f(b, nil)
		}
		for _, f := range upsB {
			f(a, nil)
		}
		return
	}
	for _, f := range downsA {
		f(b)
	}
	for _, f := range downsB {
		f(a)
	}
}

// dropReplies makes the next count replies from to to from disappear. The
// handler still runs; the requester sees the link go down.
func (n *fakeNet) dropReplies(from, to peerID, count int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.drops[linkPair{from, to}] = count
}

// onReply installs f to run when a reply reaches the caller, before the request
// resolves it. A test uses it to move the link at that instant, which makes the
// reply-versus-down race in Request deterministic.
func (n *fakeNet) onReply(f func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.replyHook = f
}

// reachedReply runs the reply hook, if a test installed one.
func (n *fakeNet) reachedReply() {
	n.mu.Lock()
	f := n.replyHook
	n.mu.Unlock()
	if f != nil {
		f()
	}
}

// onKick installs f on the Kick calls self makes towards peer.
func (n *fakeNet) onKick(self, peer peerID, f func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	key := linkPair{self, peer}
	n.kickHooks[key] = append(n.kickHooks[key], f)
}

// requests returns the frames sent from from to to, in order.
func (n *fakeNet) requests(from, to peerID) []link.Frame {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]link.Frame(nil), n.reqs[linkPair{from, to}]...)
}

// kicks returns how many times self called Kick towards peer.
func (n *fakeNet) kicks(self, peer peerID) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.kickCounts[linkPair{self, peer}]
}

// linkState reports whether the link is up and returns the channel closed when
// it goes down.
func (n *fakeNet) linkState(self, peer peerID) (up bool, down <-chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	key := pairKey(self, peer)
	ch := n.down[key]
	if ch == nil {
		ch = make(chan struct{})
		n.down[key] = ch
	}
	return n.up[key], ch
}

func (n *fakeNet) recordRequest(from, to peerID, f link.Frame) {
	n.mu.Lock()
	defer n.mu.Unlock()
	key := linkPair{from, to}
	n.reqs[key] = append(n.reqs[key], f)
}

// takeDrop consumes one lost reply for the direction from to to.
func (n *fakeNet) takeDrop(from, to peerID) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	key := linkPair{from, to}
	if n.drops[key] <= 0 {
		return false
	}
	n.drops[key]--
	return true
}

// fakeEnd is one Broker's view of the fake network. It implements deliveryLink.
type fakeEnd struct {
	net  *fakeNet
	self peerID

	mu       sync.Mutex
	handlers map[link.FrameKind]func(context.Context, peerID, link.Frame) link.Frame
	ups      []func(peerID, []byte)
	downs    []func(peerID)
}

var _ deliveryLink = (*fakeEnd)(nil)

// Request follows the link contract: not established means sent is false; once
// the frame is out, sent is true even when the reply is lost or the link drops.
func (e *fakeEnd) Request(ctx context.Context, p peerID, f link.Frame) (link.Frame, bool, error) {
	up, down := e.net.linkState(e.self, p)
	if !up {
		return link.Frame{}, false, fmt.Errorf("%w: fake link down", link.ErrNotEstablished)
	}
	e.net.recordRequest(e.self, p, f)

	handler := e.net.end(p).handler(f.Kind)
	if handler == nil {
		return link.Frame{}, true, &link.UnsupportedError{Kind: f.Kind}
	}

	// The handler runs on a context of its own, as in the real link: only the
	// connection ending cancels it, not the caller giving up on the request.
	// The cancel is registered with the network too, so a handler a test
	// abandons is ended by the test's cleanup instead of outliving it.
	handlerCtx, cancelHandler := context.WithCancel(context.Background())
	handlerID := e.net.watchHandler(cancelHandler)
	go func() {
		select {
		case <-down:
			cancelHandler()
		case <-handlerCtx.Done():
		}
	}()

	replies := make(chan link.Frame, 1)
	go func() {
		defer e.net.unwatchHandler(handlerID)
		defer cancelHandler()
		replies <- handler(handlerCtx, e.self, f)
	}()

	select {
	case reply := <-replies:
		e.net.reachedReply()
		// A link that went down before the reply was resolved wins over the
		// reply. The down may have canceled the handler, in which case the
		// frame it returned is not a reply the peer ever sent; and when the
		// down and the reply are both ready, an unordered select could
		// otherwise pick the reply and report a success for a request whose
		// link dropped.
		select {
		case <-down:
			return link.Frame{}, true, link.ErrLinkDown
		default:
		}
		if e.net.takeDrop(e.self, p) {
			e.net.setUp(e.self, p, false)
			return link.Frame{}, true, link.ErrLinkDown
		}
		return reply, true, nil
	case <-down:
		return link.Frame{}, true, link.ErrLinkDown
	case <-ctx.Done():
		return link.Frame{}, true, ctx.Err()
	}
}

func (e *fakeEnd) HandleRequest(kind link.FrameKind, h func(context.Context, peerID, link.Frame) link.Frame) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[kind] = h
}

func (e *fakeEnd) handler(kind link.FrameKind) func(context.Context, peerID, link.Frame) link.Frame {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.handlers[kind]
}

func (e *fakeEnd) Subscribe(onUp func(peerID, []byte), onDown func(peerID)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if onUp != nil {
		e.ups = append(e.ups, onUp)
	}
	if onDown != nil {
		e.downs = append(e.downs, onDown)
	}
}

func (e *fakeEnd) Established(p peerID) bool {
	up, _ := e.net.linkState(e.self, p)
	return up
}

// Kick counts the call and runs the hooks registered for this direction.
func (e *fakeEnd) Kick(p peerID) {
	n := e.net
	key := linkPair{e.self, p}
	n.mu.Lock()
	n.kickCounts[key]++
	hooks := append([]func(){}, n.kickHooks[key]...)
	n.mu.Unlock()
	for _, f := range hooks {
		f()
	}
}

func (e *fakeEnd) callbacksLocked() (ups []func(peerID, []byte), downs []func(peerID)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]func(peerID, []byte){}, e.ups...), append([]func(peerID){}, e.downs...)
}
