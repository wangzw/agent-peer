package link

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// manualTimers is an afterFunc seam for tests. It records callbacks and runs
// them only when the test calls fireAll.
type manualTimers struct {
	mu     sync.Mutex
	timers []*manualTimer
}

type manualTimer struct {
	f       func()
	fired   bool
	stopped bool
}

func (m *manualTimers) afterFunc(d time.Duration, f func()) func() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &manualTimer{f: f}
	m.timers = append(m.timers, t)
	return func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		if t.fired || t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

func (m *manualTimers) fireAll() {
	m.mu.Lock()
	var fns []func()
	for _, t := range m.timers {
		if t.fired || t.stopped {
			continue
		}
		t.fired = true
		fns = append(fns, t.f)
	}
	m.mu.Unlock()

	for _, f := range fns {
		f()
	}
}

func (m *manualTimers) pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.timers {
		if !t.fired && !t.stopped {
			n++
		}
	}
	return n
}

// newTrackedPair returns an accepted tracked server connection and the client
// side of the same local TCP connection.
func newTrackedPair(t *testing.T, reg *unauthRegistry) (*trackedConn, net.Conn) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tl := &trackingListener{Listener: ln, reg: reg}

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		c, err := tl.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- c
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		t.Fatalf("dial: %v", err)
	}

	var tc *trackedConn
	select {
	case c := <-accepted:
		tc = c.(*trackedConn)
	case err := <-acceptErr:
		_ = ln.Close()
		_ = client.Close()
		t.Fatalf("accept: %v", err)
	case <-time.After(3 * time.Second):
		_ = ln.Close()
		_ = client.Close()
		t.Fatal("timed out waiting for accept")
	}
	_ = ln.Close()

	t.Cleanup(func() {
		_ = tc.Close()
		_ = client.Close()
	})
	return tc, client
}

// expectEOF asserts that the client side observes the server connection close.
func expectEOF(t *testing.T, c net.Conn) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 1)
	_, err := c.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("client read after server close = %v, want EOF", err)
	}
}

// roundTrip writes from the client and reads the same bytes on the tracked
// connection, proving both sides are still usable.
func roundTrip(t *testing.T, tc *trackedConn, client net.Conn) {
	t.Helper()
	msg := []byte("ping")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("client write: %v", err)
	}
	if err := tc.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(tc, buf); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("server read %q, want %q", buf, msg)
	}
}

func TestFirstFrameDeadlineClosesSilentConn(t *testing.T) {
	timers := &manualTimers{}
	reg := newUnauthRegistry(64, 10*time.Second, timers.afterFunc)

	_, client := newTrackedPair(t, reg)
	if got := reg.count(); got != 1 {
		t.Fatalf("count after accept = %d, want 1", got)
	}

	timers.fireAll()

	expectEOF(t, client)
	if got := reg.count(); got != 0 {
		t.Fatalf("count after deadline = %d, want 0", got)
	}
}

func TestFirstFrameSeenStopsDeadline(t *testing.T) {
	timers := &manualTimers{}
	reg := newUnauthRegistry(64, 10*time.Second, timers.afterFunc)

	tc, client := newTrackedPair(t, reg)
	tc.firstFrameSeen()

	timers.fireAll()

	roundTrip(t, tc, client)
	if got := reg.count(); got != 1 {
		t.Fatalf("count after first frame = %d, want 1", got)
	}
}

func TestCapEvictsOldest(t *testing.T) {
	timers := &manualTimers{}
	reg := newUnauthRegistry(2, 10*time.Second, timers.afterFunc)

	_, c1 := newTrackedPair(t, reg)
	tc2, c2 := newTrackedPair(t, reg)
	tc3, c3 := newTrackedPair(t, reg)

	expectEOF(t, c1)
	if got := reg.count(); got != 2 {
		t.Fatalf("count at cap = %d, want 2", got)
	}
	roundTrip(t, tc2, c2)
	roundTrip(t, tc3, c3)
}

func TestAuthenticatedLeavesRegistry(t *testing.T) {
	timers := &manualTimers{}
	reg := newUnauthRegistry(2, 10*time.Second, timers.afterFunc)

	tc1, c1 := newTrackedPair(t, reg)
	tc1.authenticated()
	if got := reg.count(); got != 0 {
		t.Fatalf("count after authentication = %d, want 0", got)
	}
	if got := timers.pending(); got != 0 {
		t.Fatalf("pending deadlines after authentication = %d, want 0", got)
	}

	newTrackedPair(t, reg)
	newTrackedPair(t, reg)
	if got := reg.count(); got != 2 {
		t.Fatalf("count with two unauthenticated conns = %d, want 2", got)
	}

	roundTrip(t, tc1, c1)
}

func TestClaimStreamOnce(t *testing.T) {
	timers := &manualTimers{}
	reg := newUnauthRegistry(64, 10*time.Second, timers.afterFunc)

	tc, _ := newTrackedPair(t, reg)
	if !tc.claimStream() {
		t.Fatal("first claimStream = false, want true")
	}
	if tc.claimStream() {
		t.Fatal("second claimStream = true, want false")
	}

	concurrent, _ := newTrackedPair(t, reg)
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if concurrent.claimStream() {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("concurrent claimStream winners = %d, want 1", got)
	}
}
