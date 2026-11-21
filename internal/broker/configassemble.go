package broker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
)

// configWiring mounts the configuration unit onto the running Broker: the
// access decision on every Local call, the broker.listen validator, and the
// subscriptions that forward configuration changes to link, directory and the
// local agent table. It runs last, so the directory assembler has already
// filled Deps.CardIssuer and the link exists in the Assembly.
type configWiring struct {
	// checker decides access to the configuration file. nil: asm.Access, and
	// when that is nil too, the user-mode allowAllAccess{} (only allowed when
	// the run is not the system form).
	checker accessChecker
}

var _ Assembler = (*configWiring)(nil)

// Name is the assembler's name in startup errors.
func (*configWiring) Name() string { return "config" }

// Assemble appends the two access interceptors to the server options New will
// apply, and returns the component that registers the validator and the
// subscriptions once the Broker exists.
func (w *configWiring) Assemble(_ context.Context, cfg ConfigSource, deps *Deps, asm *Assembly) ([]Component, error) {
	checker := w.checker
	if checker == nil {
		checker = asm.Access
		if checker == nil {
			if asm.System {
				return nil, errors.New("system mode requires an access checker in the Assembly")
			}
			checker = allowAllAccess{}
		}
	}
	if asm.System && checker != asm.Access {
		return nil, errors.New("system mode must use the Assembly access checker")
	}
	asm.configAssembled = true
	asm.effectiveAccess = checker
	path := func() string { return cfg.Paths().ConfigFile }
	deps.ServerOptions = append(deps.ServerOptions,
		grpc.ChainUnaryInterceptor(accessUnaryInterceptor(checker, path)),
		grpc.ChainStreamInterceptor(accessStreamInterceptor(checker, path)),
	)

	var l linkControl = noopLink{}
	if asm.Link != nil {
		l = asm.Link
	}
	var d CardIssuer = noopIssuer{}
	if deps.CardIssuer != nil {
		d = deps.CardIssuer
	}

	return []Component{&configComponent{cfg: cfg, link: l, cards: d}}, nil
}

// configComponent registers the configuration callbacks. Start runs after New
// and before the link listener and the local socket are bound, so the validator
// and the subscriptions exist before any local call can arrive.
type configComponent struct {
	cfg   ConfigSource
	link  linkControl
	cards CardIssuer
	log   *slog.Logger

	// ready is set by AfterReady. Until then broker.listen changes are not
	// forwarded to the link; see preReadyLink.
	ready atomic.Bool

	// stopped is set by Stop. The subscriptions registered by Start then drop
	// changes instead of forwarding them to components that are closing.
	stopped atomic.Bool

	// suppressed is set while Run's listen retry owns binding: the component
	// must not bind the address a second time from a broker.listen change.
	suppressed atomic.Bool

	// boundMu guards bound and boundKnown, which record the address the link is
	// listening on: the startup bind Run reported, then every successful
	// rebind. Holding it across Listen also serializes rebinds.
	boundMu    sync.Mutex
	bound      string
	boundKnown bool
}

var (
	_ Component          = (*configComponent)(nil)
	_ PostReadyComponent = (*configComponent)(nil)
)

// Name is the component's name in startup errors.
func (*configComponent) Name() string { return "config" }

// Start registers the broker.listen validator and the change subscriptions.
func (c *configComponent) Start(_ context.Context, b *Broker) error {
	c.log = b.Logger()
	c.cfg.Validate("broker.listen", listenProbeCheck(c.cfg.ProbeListen))
	subscribeConfigChanges(c.cfg, preReadyLink{linkControl: c.link, comp: c}, c.cards, b, b.Logger(), &c.stopped)
	return nil
}

// AfterReady enables broker.listen rebinds and applies the current value once.
// Run has bound the startup listener by now, but a change delivered between
// Run's read and this point was dropped by preReadyLink, so the component
// reconciles the link with the effective value. rebind skips the call when the
// startup bind already used that address.
func (c *configComponent) AfterReady(context.Context) error {
	c.ready.Store(true)
	if !c.isBound() {
		return nil
	}
	v, err := c.cfg.Get("broker.listen")
	if err != nil {
		return err
	}
	if err := c.rebind(v.String); err != nil {
		logListenRebindFailure(c.log, v.String, err)
	}
	return nil
}

// listenBound records the address Run bound at startup, so AfterReady can tell
// a change that arrived during startup from the value that is already bound. It
// implements listenBoundReporter.
func (c *configComponent) listenBound(address string) {
	c.boundMu.Lock()
	c.bound = address
	c.boundKnown = true
	c.boundMu.Unlock()
}

// isBound reports whether Run has reported a startup bind.
func (c *configComponent) isBound() bool {
	c.boundMu.Lock()
	defer c.boundMu.Unlock()
	return c.boundKnown
}

// rebind binds address unless the link already listens there. A notification
// for a change that was already applied, such as one delivered asynchronously
// after AfterReady reconciled the same value, is skipped instead of binding the
// address a second time and failing with EADDRINUSE.
func (c *configComponent) rebind(address string) error {
	if c.suppressed.Load() {
		return nil
	}
	c.boundMu.Lock()
	defer c.boundMu.Unlock()
	if c.boundKnown && c.bound == address {
		return nil
	}
	if err := c.link.Listen(address); err != nil {
		return err
	}
	c.bound = address
	c.boundKnown = true
	return nil
}

// suppressListenRebind turns the component's own broker.listen rebinding off
// or on. Run turns it off while its listen retry owns the address and back on
// after the retry binds successfully.
func (c *configComponent) suppressListenRebind(suppress bool) {
	c.suppressed.Store(suppress)
}

// Stop stops forwarding configuration changes. A change config delivers after
// Stop, while the link is closing, is dropped instead of reaching the listener
// and logging a rebind failure.
func (c *configComponent) Stop() { c.stopped.Store(true) }

// preReadyLink drops broker.listen rebinds until configComponent.AfterReady.
// Run reads the effective broker.listen value after every component starts and
// binds it, so a change delivered while the components start is already
// reflected by that bind. Forwarding it as well would bind the address early
// and make Run's own bind fail with "address already in use". AfterReady then
// applies a change delivered after Run's read.
type preReadyLink struct {
	linkControl
	comp *configComponent
}

func (l preReadyLink) Listen(address string) error {
	if !l.comp.ready.Load() {
		return nil
	}
	return l.comp.rebind(address)
}

// noopLink stands in for link when the assembly has none, as in unit tests that
// do not build the link stack.
type noopLink struct{}

func (noopLink) Listen(string) error { return nil }
func (noopLink) AnnounceAlias()      {}
func (noopLink) AnnounceAddresses()  {}

var _ linkControl = noopLink{}

// noopIssuer stands in for the directory card when the assembly has no issuer.
type noopIssuer struct{}

func (noopIssuer) Reissue() {}

var _ CardIssuer = noopIssuer{}
