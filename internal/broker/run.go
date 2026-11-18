package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/inbox"
)

// The startup failures listed in design-broker.md. Run wraps each with the
// concrete cause, so cmd can pick an exit code with errors.Is while the
// readiness pipe gets the full text as one line.
var (
	// ErrNotRoot is returned by Run when --system is used without root.
	ErrNotRoot           = errors.New("the system Broker must run as root")
	ErrInSession         = errors.New("the Broker must not run inside the process tree of a Claude Code session")
	ErrRuntimeDir        = errors.New("the runtime directory is not usable")
	ErrInvalidConfig     = errors.New("the configuration is invalid")
	ErrInvalidListen     = errors.New("the --listen address is invalid")
	ErrListenUnavailable = errors.New("the listen address is unavailable")
	// ErrSystemModeUnavailable is no longer returned by Run; kept for callers
	// that map exit codes.
	ErrSystemModeUnavailable = errors.New("the system Broker is not available in this build")
	ErrLockLost              = errors.New("the account lock file was replaced; exiting so that only one Broker runs")
)

// evLocalServeFailed records an error returned by the local gRPC server. It is
// not a startup failure: the Broker keeps running, and only the listener that
// failed stops accepting.
const evLocalServeFailed = "local_serve_failed"

// RunOptions is the process entry point's input to Run. The exported fields
// mirror the command line; checkTick and afterCheck exist only so tests can
// drive the periodic runtime-directory review without waiting a minute.
type RunOptions struct {
	System               bool
	Listen               string
	Ready                io.WriteCloser
	LogOutput            io.Writer
	Version              string
	LocalProtocolVersion uint32      // 0: localpb.ProtocolVersion
	LinkProtocolVersion  uint32      // 0: linkpb.ProtocolVersion
	Assemblers           []Assembler // nil: DefaultAssemblers()
	Components           []Component
	Deps                 Deps

	checkTick  <-chan time.Time // tests only; nil: time.NewTicker(time.Minute)
	afterCheck func()           // tests only; called after every periodic check
}

// Run starts a user-level Broker and blocks until ctx is cancelled or a
// periodic check fails. The startup sequence is Load, the assemblers, New, the
// component Start methods, binding the link listener, binding the local
// socket, writing "ok", starting link, and the AfterReady methods; a failure at
// any point is written to the readiness pipe as one line and unwinds everything
// acquired so far.
func Run(ctx context.Context, opts RunOptions) error {
	// Step 0: defaults. The logger is created here rather than inside New
	// because the assemblers log through deps.Logger and they run before New.
	deps := opts.Deps

	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	if deps.Logger == nil {
		out := opts.LogOutput
		if out == nil {
			out = os.Stderr
		}
		deps.Logger = NewLogger(out, level)
		slog.SetDefault(deps.Logger)
	}
	if deps.Clock == nil {
		deps.Clock = SystemClock()
	}
	if deps.Config == nil {
		deps.Config = PackageConfig()
	}
	if deps.InSession == nil {
		deps.InSession = inbox.InSession
	}
	deps.LocalProtocolVersion = opts.LocalProtocolVersion
	cfg := deps.Config

	var (
		lock      *AccountLock
		sock      *LocalSocket
		b         *Broker
		asm       = &Assembly{}
		started   []Component
		loaded    bool
		runnerRan bool
	)

	stopComponents := func(comps []Component) {
		for i := len(comps) - 1; i >= 0; i-- {
			comps[i].Stop()
		}
	}

	fail := func(err error) error {
		deps.Logger.Error(EvStartupFailed, "error", err.Error())
		writeReadyLine(opts.Ready, err.Error())
		// Cancel received-request processing before link Close waits for its
		// handlers; a blocked inbox write must not hold shutdown open.
		if deps.Delivery != nil {
			deps.Delivery.stop()
		}
		if asm.Runner != nil && runnerRan {
			asm.Runner.Close()
		}
		stopComponents(started)
		started = nil
		if sock != nil {
			_ = sock.Close(true)
		}
		if loaded {
			cfg.Close()
		}
		if lock != nil {
			lock.Release()
		}
		return err
	}

	// Step 1: the system form requires root. The steps below then use the
	// system paths and the accounts on this host, so this check stays before
	// the account lock and the configuration load.
	if opts.System {
		geteuid := opts.Deps.Geteuid
		if geteuid == nil {
			geteuid = os.Geteuid
		}
		if euid := geteuid(); euid != 0 {
			return fail(fmt.Errorf("%w: --system requires root (euid %d)", ErrNotRoot, euid))
		}
	}

	// Step 2: never run inside a Claude Code session's process tree.
	if deps.InSession() {
		return fail(fmt.Errorf("%w: CLAUDE_CODE_MESSAGING_SOCKET is set", ErrInSession))
	}

	// Step 3: prepare the runtime directory and take the account lock. The
	// lock is taken before Load so that a second instance fails before it
	// touches any configuration.
	if err := cfg.CheckRuntimeDir(); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrRuntimeDir, err))
	}
	l, err := AcquireAccountLock(cfg.AccountLock())
	if err != nil {
		return fail(err)
	}
	lock = l

	// Step 4: load the configuration and follow log.level.
	mode := config.ModeUser
	if opts.System {
		mode = config.ModeSystem
	}
	if err := cfg.Load(mode, opts.Listen); err != nil {
		if opts.Listen != "" && errors.Is(err, config.ErrInvalidValue) && strings.Contains(err.Error(), "broker.listen") {
			return fail(fmt.Errorf("%w: %v", ErrInvalidListen, err))
		}
		return fail(fmt.Errorf("%w: %v", ErrInvalidConfig, err))
	}
	loaded = true

	// SetConfig reads this to know whether the run started with --listen;
	// set it only after Load accepted the value.
	deps.ListenOverride = opts.Listen

	if v, err := cfg.Get("log.level"); err == nil && v.Kind == config.KindString {
		if lv, err := ParseLogLevel(v.String); err == nil {
			level.Set(lv)
		}
	}
	cfg.Subscribe("log.level", func(_, newVal config.Value) {
		if newVal.Kind != config.KindString {
			return
		}
		lv, err := ParseLogLevel(newVal.String)
		if err != nil {
			// Keep the current level for an invalid value.
			return
		}
		level.Set(lv)
		deps.Logger.Info(EvLogLevelChanged, "level", newVal.String)
	})

	// System mode: the runtime directory and the local socket are shared by
	// every account, so their group and mode come from the configuration. The
	// group is checked before anything binds, and both keys are watched so a
	// change is applied to the socket that is already there.
	sockPath := filepath.Join(cfg.Paths().RuntimeDir, "broker.sock")
	var applySocketPerms func() error
	if opts.System {
		applySocketPerms = func() error {
			group, err := cfg.Get("broker.local_socket_group")
			if err != nil {
				return err
			}
			mode, err := cfg.Get("broker.local_socket_mode")
			if err != nil {
				return err
			}
			return applyLocalSocketPerms(sockPath, cfg.Paths().RuntimeDir, group.String, mode.String)
		}
		cfg.Validate("broker.local_socket_group", validateSocketGroup)
		if err := checkSocketGroupAtStartup(cfg); err != nil {
			return fail(err)
		}
		watchSocketPerms(cfg, applySocketPerms, deps.Logger)
	}

	// Step 5: run the assemblers, then append the options' components.
	assemblers := opts.Assemblers
	if assemblers == nil {
		assemblers = DefaultAssemblers()
	}
	asm.LinkProtocolVersion = opts.LinkProtocolVersion
	var assembled []Component
	for _, a := range assemblers {
		comps, err := a.Assemble(ctx, cfg, &deps, asm)
		if err != nil {
			// Components from earlier assemblers were built and must be
			// released even though none was started.
			stopComponents(assembled)
			return fail(fmt.Errorf("%s: %w", a.Name(), err))
		}
		assembled = append(assembled, comps...)
	}
	components := make([]Component, 0, len(assembled)+len(opts.Components))
	components = append(components, assembled...)
	components = append(components, opts.Components...)

	// Step 6: construct the Broker from the (possibly modified) Deps.
	nb, err := New(deps)
	if err != nil {
		stopComponents(assembled)
		return fail(err)
	}
	b = nb

	// Step 7: start the components in order. A failing component is not
	// stopped; the ones before it are.
	for _, c := range components {
		if err := c.Start(ctx, b); err != nil {
			return fail(fmt.Errorf("%s: %w", c.Name(), err))
		}
		started = append(started, c)
	}

	// Step 8: bind the link listener, using the effective broker.listen. An
	// empty address skips this step.
	if asm.Runner != nil {
		v, err := cfg.Get("broker.listen")
		if err != nil {
			return fail(fmt.Errorf("%w: %v", ErrInvalidConfig, err))
		}
		if v.String != "" {
			runnerRan = true
			if err := asm.Runner.Listen(v.String); err != nil {
				return fail(fmt.Errorf("%w: %s: %v", ErrListenUnavailable, v.String, err))
			}
		}
		// Tell the components that forward broker.listen what is bound, so a
		// change delivered before their AfterReady is reconciled against it.
		for _, c := range started {
			if r, ok := c.(listenBoundReporter); ok {
				r.listenBound(v.String)
			}
		}
	}

	// Step 9: bind the local socket. Only the lock holder reaches this point.
	s, err := BindLocalSocket(sockPath, 0o600)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrRuntimeDir, err))
	}
	sock = s
	if opts.System {
		if err := applySocketPerms(); err != nil {
			_ = s.Close(true)
			return fail(fmt.Errorf("%w: %v", ErrInvalidConfig, err))
		}
	}

	// Step 10: the readiness pipe reports success only after both listeners
	// exist. The local service is served from here on.
	writeReadyLine(opts.Ready, "ok")
	deps.Logger.Info(EvBrokerStarted, "socket", sockPath, "pid", os.Getpid(), "version", opts.Version)
	go serveLocal(b, sock.Listener(), deps.Logger)

	// Step 11: start link after the local socket is ready.
	if asm.Runner != nil {
		asm.Runner.Start()
		runnerRan = true
	}

	shutdown := func() {
		deps.Logger.Info(EvBrokerStopping)
		// Cancel received-request processing before link Close waits for its
		// handlers; a blocked inbox write must not hold shutdown open.
		if deps.Delivery != nil {
			deps.Delivery.stop()
		}
		if asm.Runner != nil {
			asm.Runner.Close()
		}
		b.Stop()
		stopComponents(started)
		_ = sock.Close(true)
		cfg.Close()
		lock.Release()
	}

	// Step 12: components that need the ready socket and link run now.
	for _, c := range components {
		pc, ok := c.(PostReadyComponent)
		if !ok {
			continue
		}
		if err := pc.AfterReady(ctx); err != nil {
			deps.Logger.Error("after_ready_failed", "component", c.Name(), "error", err)
			shutdown()
			return err
		}
	}

	// Step 13: run until cancellation, checking the lock and the local socket
	// once per tick.
	tick := opts.checkTick
	if tick == nil {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			shutdown()
			return nil
		case <-tick:
			if !lock.StillHeld() {
				deps.Logger.Error(EvLockLost)
				shutdown()
				return ErrLockLost
			}
			if !sock.Present() {
				ns, err := BindLocalSocket(sockPath, 0o600)
				if err != nil {
					deps.Logger.Warn(EvLocalSocketRebindFailed, "error", err)
				} else {
					if opts.System {
						if err := applySocketPerms(); err != nil {
							deps.Logger.Error(evLocalSocketPermsFailed, "error", err.Error())
						}
					}
					old := sock
					sock = ns
					go serveLocal(b, ns.Listener(), deps.Logger)
					_ = old.Close(false)
					deps.Logger.Info(EvLocalSocketRebound, "socket", sockPath)
				}
			}
			if opts.afterCheck != nil {
				opts.afterCheck()
			}
		}
	}
}

// serveLocal serves the local interface and records an unexpected error. A
// closed listener, from Stop or from a rebind, is expected and ignored.
func serveLocal(b *Broker, ln net.Listener, log *slog.Logger) {
	err := b.Serve(ln)
	if err == nil || errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
		return
	}
	log.Warn(evLocalServeFailed, "error", err)
}

// writeReadyLine writes one line to the readiness pipe and closes it. A nil
// pipe, as when the process was started from a terminal, is a no-op.
func writeReadyLine(w io.WriteCloser, text string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, strings.ReplaceAll(text, "\n", " ")+"\n")
	_ = w.Close()
}
