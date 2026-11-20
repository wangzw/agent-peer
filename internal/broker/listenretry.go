package broker

import (
	"log/slog"
	"time"
)

// listenRetrier retries binding broker.listen with exponential backoff while
// the Broker runs local only. The first wait is one second, each later wait
// doubles and the wait is capped at one minute. A broker.listen change wakes
// it immediately and resets the backoff. A successful bind ends it.
type listenRetrier struct {
	bind        func(addr string) error
	currentAddr func() (string, error)
	clock       Clock
	log         *slog.Logger
	onSuccess   func(addr string) (string, error)
	onFailure   func(addr, bound string, err error)

	wake   chan struct{}
	stopCh chan struct{}
	done   chan struct{}
}

const (
	listenRetryFirst = time.Second
	listenRetryMax   = time.Minute
)

func newListenRetrier(bind func(string) error, currentAddr func() (string, error), clock Clock, log *slog.Logger,
	onSuccess func(string) (string, error), onFailure func(addr, bound string, err error)) *listenRetrier {
	if clock == nil {
		clock = SystemClock()
	}
	return &listenRetrier{
		bind:        bind,
		currentAddr: currentAddr,
		clock:       clock,
		log:         log,
		onSuccess:   onSuccess,
		onFailure:   onFailure,
		wake:        make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// start begins the retry loop. It is called once.
func (r *listenRetrier) start() {
	go r.run()
}

// retryNow asks for an immediate attempt and resets the backoff. A change
// delivered while an attempt runs is retained for the next wait.
func (r *listenRetrier) retryNow() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// stop ends the retry loop and waits for it. It is safe to call once.
func (r *listenRetrier) stop() {
	if r == nil {
		return
	}
	close(r.stopCh)
	<-r.done
}

func (r *listenRetrier) run() {
	defer close(r.done)
	delay := listenRetryFirst
	lastReason := ""
	// bound is the address a successful bind left listening. It stays put when
	// a later reconcile fails, so the failure can say the old listener still
	// serves.
	bound := ""
	for {
		fired, stopped := r.wait(delay)
		if stopped {
			return
		}
		addr, err := r.currentAddr()
		if err == nil {
			err = r.bind(addr)
		}
		if err == nil {
			bound = addr
			if r.onSuccess != nil {
				// onSuccess reconciles the bound address with the current
				// value after enabling the component's rebinding. A failure
				// there keeps the retrier running and names the address that
				// failed.
				next, successErr := r.onSuccess(addr)
				if successErr != nil {
					err = successErr
					if next != "" {
						addr = next
					}
				} else {
					if next != "" {
						addr = next
					}
					bound = addr
				}
			}
		}
		if err == nil {
			return
		}
		reason := addr + ": " + err.Error()
		if reason != lastReason {
			lastReason = reason
			if r.onFailure != nil {
				r.onFailure(addr, bound, err)
			}
		}
		if fired {
			delay *= 2
			if delay > listenRetryMax {
				delay = listenRetryMax
			}
		} else {
			// woken by a broker.listen change: the immediate attempt above
			// already used the new address, and the next wait starts over.
			delay = listenRetryFirst
		}
	}
}

// wait blocks for d or until a change wakes the loop or it is stopped. fired
// reports that d elapsed; stopped reports that the retrier is shutting down.
func (r *listenRetrier) wait(d time.Duration) (fired, stopped bool) {
	ch := make(chan struct{}, 1)
	t := r.clock.AfterFunc(d, func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	})
	select {
	case <-r.stopCh:
		t.Stop()
		return false, true
	case <-r.wake:
		t.Stop()
		return false, false
	case <-ch:
		return true, false
	}
}
