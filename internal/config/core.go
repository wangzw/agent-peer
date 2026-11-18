package config

import (
	"log/slog"
	"net/netip"
	"os"
	"os/user"
	"sync"
	"time"
)

// ticker is the small part of time.Ticker this package uses.
type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
func (t realTicker) Stop()               { t.Ticker.Stop() }

func newRealTicker(d time.Duration) ticker { return realTicker{time.NewTicker(d)} }

// addressSource supplies the addresses of the interface that carries the
// default route.
type addressSource interface {
	DefaultRouteAddresses() ([]netip.Addr, error)
}

// core holds all state and dependencies. Package-level functions only fetch the
// singleton and call a method on it, so tests can build an independent core.
type core struct {
	mu sync.Mutex

	mode   Mode
	paths  PathSet
	values map[string]Value

	effectiveListen listenAddr
	listenOverride  bool

	explicitAlias bool

	advertised  []AdvertisedAddr
	routes      []netip.Addr
	routesValid bool

	validators map[string][]func(old, new Value) error
	subs       map[string][]func(old, new Value)
	advSubs    []func(old, new []AdvertisedAddr)

	notify notifier

	// auditMu guards the audit pointer. Audit and ReadAudit run without
	// core.mu, while close clears the pointer, so the pointer needs its own
	// lock.
	auditMu sync.RWMutex
	audit   *auditLog

	watch *configWatcher

	file              *configDocument
	lastFileHash      [32]byte
	lastIgnoredListen string

	closed        bool
	noBackground  bool
	skipDataCheck bool

	addrTicker     ticker
	addrTickerDone chan struct{}

	now          func() time.Time
	newTicker    func(time.Duration) ticker
	hostname     func() (string, error)
	homeDir      func() (string, error)
	euid         func() int
	routesSrc    addressSource
	writeFile    func(path string, data []byte, mode os.FileMode) error
	readFile     func(path string) ([]byte, error)
	statFile     func(path string) (os.FileInfo, error)
	logger       *slog.Logger
	checkRuntime func() (string, error)
}

var defaultLogger = func() *slog.Logger { return slog.Default() }

func accountHomeDir() (string, error) {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return os.UserHomeDir()
}

func newCore() *core {
	hostname := os.Hostname
	return &core{
		values:     make(map[string]Value),
		validators: make(map[string][]func(old, new Value) error),
		subs:       make(map[string][]func(old, new Value)),
		now:        time.Now,
		newTicker:  newRealTicker,
		hostname:   hostname,
		homeDir:    accountHomeDir,
		euid:       os.Geteuid,
		routesSrc:  platformAddressSource{},
		writeFile:  writeConfigFile,
		readFile:   os.ReadFile,
		statFile:   os.Stat,
		logger:     defaultLogger(),
	}
}

// hostnameString returns the sanitized hostname without taking the lock; the
// caller must hold it or be initializing the core.
func (c *core) hostnameString() string {
	return sanitizeHostnameFrom(c.hostname, c.logger)
}

func sanitizeHostnameFrom(fn func() (string, error), logger *slog.Logger) string {
	name, err := fn()
	if err != nil {
		logger.Error("config: cannot read the system hostname", "error", err)
		return ""
	}
	return sanitizeHostname(name)
}

// close stops every goroutine and open file. It is idempotent.
func (c *core) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	w := c.watch
	stop := c.addrTickerDone
	t := c.addrTicker
	c.watch = nil
	c.addrTicker = nil
	notify := c.notify
	c.auditMu.Lock()
	audit := c.audit
	c.audit = nil
	c.auditMu.Unlock()
	c.mu.Unlock()

	if w != nil {
		w.stop()
	}
	if t != nil {
		t.Stop()
	}
	if stop != nil {
		close(stop)
	}
	if notify != nil {
		notify.close()
	}
	if audit != nil {
		_ = audit.close()
	}
}
