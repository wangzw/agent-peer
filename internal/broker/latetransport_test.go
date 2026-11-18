package broker

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/wangzw/agent-peer/internal/pairing"
)

// recordingTarget is the minimal forwarding target these tests need: it
// records each dial and drop and runs whatever dial callback it is handed.
// Its methods take a mutex because the concurrency test calls them from many
// goroutines.
type recordingTarget struct {
	mu      sync.Mutex
	address string
	dials   int
	dropped []pairing.Fingerprint
}

func (r *recordingTarget) PairDial(_ context.Context, address string, run func(pairing.PairConn) error) error {
	r.mu.Lock()
	r.address = address
	r.dials++
	r.mu.Unlock()
	return run(nil)
}

func (r *recordingTarget) Drop(fp pairing.Fingerprint) {
	r.mu.Lock()
	r.dropped = append(r.dropped, fp)
	r.mu.Unlock()
}

func (r *recordingTarget) gotAddress() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.address
}

func (r *recordingTarget) gotDropped() []pairing.Fingerprint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pairing.Fingerprint(nil), r.dropped...)
}

func TestLateTransportBeforeSet(t *testing.T) {
	tr := &lateTransport{}

	called := false
	err := tr.PairDial(context.Background(), "h:1", func(pairing.PairConn) error {
		called = true
		return nil
	})
	if !errors.Is(err, pairing.ErrDialFailed) {
		t.Fatalf("PairDial error = %v, want it to wrap %v", err, pairing.ErrDialFailed)
	}
	if called {
		t.Fatal("PairDial ran the dial callback before a target was set")
	}

	tr.Drop(pairing.Fingerprint{3}) // must be a no-op, not a panic
}

func TestLateTransportForwards(t *testing.T) {
	tr := &lateTransport{}
	target := &recordingTarget{}
	tr.set(target)

	ran := false
	err := tr.PairDial(context.Background(), "h:1", func(pairing.PairConn) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("PairDial: %v", err)
	}
	if got := target.gotAddress(); got != "h:1" {
		t.Fatalf("target got address %q, want %q", got, "h:1")
	}
	if !ran {
		t.Fatal("target did not run the dial callback")
	}

	fp := pairing.Fingerprint{3}
	tr.Drop(fp)
	dropped := target.gotDropped()
	if len(dropped) != 1 || dropped[0] != fp {
		t.Fatalf("target got drops %v, want one drop of %v", dropped, fp)
	}
}

func TestLateTransportSetTwicePanics(t *testing.T) {
	tr := &lateTransport{}
	tr.set(&recordingTarget{})

	defer func() {
		const want = "broker: transport target set twice"
		got := recover()
		if got != want {
			t.Fatalf("second set panicked with %v, want %q", got, want)
		}
	}()
	tr.set(&recordingTarget{})
}

func TestLateTransportConcurrentSetAndUse(t *testing.T) {
	tr := &lateTransport{}
	target := &recordingTarget{}
	fp := pairing.Fingerprint{3}

	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		tr.set(target)
	}()

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				_ = tr.PairDial(context.Background(), "h:1", func(pairing.PairConn) error { return nil })
				tr.Drop(fp)
			}
		}()
	}

	close(start)
	wg.Wait()
}
