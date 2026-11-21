package broker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/wangzw/peers/internal/config"
	"github.com/wangzw/peers/internal/directory"
	"github.com/wangzw/peers/internal/pairing"
)

// directoryHandle is the directory's lifecycle on top of the operations the
// adapter forwards. *directory.Directory implements it, and so does the test
// double discoveryDir.
type directoryHandle interface {
	directoryOps
	Start() error
	Close()
	OnCard(f func(fp pairing.Fingerprint, addrs []config.AdvertisedAddr))
	RetireInactiveTopics()
}

// directoryAssembler opens the directory over the pairing identity and link
// built by earlier assemblers, installs the adapter into Deps, and returns the
// component that starts and stops the directory.
//
// open and newIdentity are fields so unit tests can substitute a directory
// double and an identity source; nil selects the production functions.
type directoryAssembler struct {
	open        func(directory.Deps) (directoryHandle, error)
	newIdentity func(*pairing.Pairing) (directory.Identity, error)
}

var (
	_ Assembler = (*directoryAssembler)(nil)

	_ directoryHandle = (*directory.Directory)(nil)
)

// Name is the assembler's name in startup errors.
func (a *directoryAssembler) Name() string { return "directory" }

// Assemble opens the directory when the Broker has a pairing identity and a
// link. Without either it installs nothing and the Broker runs with its
// built-in "not public" behavior. An identity error fails startup: a Broker
// configured to take part in the directory must not silently run without one.
func (a *directoryAssembler) Assemble(_ context.Context, cfg ConfigSource, deps *Deps, asm *Assembly) ([]Component, error) {
	if asm.Pairing == nil || asm.Link == nil {
		return nil, nil
	}
	newIdentity := a.newIdentity
	if newIdentity == nil {
		newIdentity = directory.NewIdentity
	}
	id, err := newIdentity(asm.Pairing)
	if err != nil {
		return nil, fmt.Errorf("directory identity: %w", err)
	}
	dd := directory.Deps{
		Identity: id,
		Peers:    asm.Pairing,
		Links:    directory.NewLinkTransport(asm.Link),
		Logger:   deps.Logger,
		Now:      wallClock,
	}
	return a.assembleWith(dd, asm.Pairing, cfg, deps)
}

// openDirectory is the production open function. On failure it returns an
// untyped nil handle, never a typed nil pointer.
func openDirectory(d directory.Deps) (directoryHandle, error) {
	dir, err := directory.Open(d)
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// assembleWith opens the directory described by dd, subscribes the card
// relay, installs the adapter into deps and returns the component.
func (a *directoryAssembler) assembleWith(dd directory.Deps, cp cardPairing, cfg ConfigSource, deps *Deps) ([]Component, error) {
	open := a.open
	if open == nil {
		open = openDirectory
	}
	d, err := open(dd)
	if err != nil {
		// The directory file is the only file Open reads from the data
		// directory. A failure to read it wraps a *fs.PathError; a wiring
		// error such as a missing identity, "links is required" or "config has
		// no data directory" does not, and stays fatal.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = wrapDataUnavailable(dataKindDirectory, err)
		}
		return nil, fmt.Errorf("opening the directory: %w", err)
	}
	d.OnCard(func(fp pairing.Fingerprint, addrs []config.AdvertisedAddr) {
		relayCard(cp, fp, addrs)
	})
	ad := newDirectoryAdapter(d)
	ad.install(deps)
	return []Component{&directoryComponent{d: d, a: ad, cfg: cfg}}, nil
}

// directoryComponent starts and stops the directory and keeps the public
// switch in step with broker.public. After the Broker is ready it schedules the
// one-time retirement of declarations left over from the previous run.
type directoryComponent struct {
	d   directoryHandle
	a   *directoryAdapter
	cfg ConfigSource

	// afterFunc schedules the retirement timer; tests substitute a recorder.
	afterFunc func(time.Duration, func()) interface{ Stop() bool }

	// publicMu serializes the startup read of broker.public with the
	// configuration subscriber, so an older value cannot overwrite a newer one.
	publicMu sync.Mutex

	mu    sync.Mutex
	timer interface{ Stop() bool }
}

var (
	_ Component          = (*directoryComponent)(nil)
	_ PostReadyComponent = (*directoryComponent)(nil)
)

// Name is the component's name in startup and shutdown logs.
func (c *directoryComponent) Name() string { return "directory" }

// Start starts the directory and wires the public switch. It runs before the
// link listens, so the directory registers its frame handlers and link
// subscription in time.
func (c *directoryComponent) Start(_ context.Context, b *Broker) error {
	return c.startWith(b.publicTopicSet, b.Logger())
}

// startWith is Start with its Broker dependencies passed in, so tests can
// supply a fixed topic set and logger without constructing a Broker.
func (c *directoryComponent) startWith(topics func() map[string]string, log *slog.Logger) error {
	if err := c.d.Start(); err != nil {
		return fmt.Errorf("starting the directory: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	// The subscriber ignores the value carried by the notification and re-reads
	// the current configuration while holding publicMu. A queued older
	// notification therefore cannot restore a value that has since changed.
	c.cfg.Subscribe("broker.public", func(_, _ config.Value) {
		c.applyCurrentPublic(func(public bool) {
			publicChanged(c.a, topics, public, log)
		})
	})
	// A change between the configuration load and the subscription above would
	// not reach the subscriber, so read the current value once under the same
	// lock. A missing key leaves the directory at its "not public" default; a
	// failing SetPublic is logged and does not fail startup, because the broker
	// remains usable while not public.
	c.applyCurrentPublic(func(public bool) {
		if err := c.a.SetPublic(public); err != nil {
			logSetPublicFailure(log, public, err)
		}
	})
	return nil
}

// applyCurrentPublic reads broker.public under publicMu and hands the value to
// apply while still holding the lock. The startup path and the configuration
// subscriber use it, so their reads and applications are serialized.
func (c *directoryComponent) applyCurrentPublic(apply func(bool)) {
	c.publicMu.Lock()
	defer c.publicMu.Unlock()
	v, err := c.cfg.Get("broker.public")
	if err != nil {
		return
	}
	apply(v.Bool)
}

// AfterReady schedules the retirement of public topic declarations left over
// from the previous run. The two-minute delay leaves time for restarted agents
// to declare their topics again; declarations still inactive afterwards are
// withdrawn.
func (c *directoryComponent) AfterReady(context.Context) error {
	afterFunc := c.afterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, f func()) interface{ Stop() bool } {
			return time.AfterFunc(d, f)
		}
	}
	c.mu.Lock()
	c.timer = afterFunc(2*time.Minute, c.d.RetireInactiveTopics)
	c.mu.Unlock()
	return nil
}

// Stop cancels the retirement timer before closing the directory, so a timer
// firing after shutdown cannot touch a closed directory.
func (c *directoryComponent) Stop() {
	c.mu.Lock()
	timer := c.timer
	c.timer = nil
	c.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	c.d.Close()
}
