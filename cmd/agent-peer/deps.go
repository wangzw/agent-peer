package main

import (
	"context"
	"io"
	"net/http"

	"github.com/wangzw/agent-peer/internal/broker/update"
)

// brokerOptions is the subset of the Broker's run options that the command
// line controls. It keeps cmd independent of the broker package so that the
// subcommands can be built and tested before the Broker's entry point exists.
type brokerOptions struct {
	System    bool
	Listen    string
	Version   string
	LogOutput io.Writer
}

// startFailure classifies a Broker startup error into the exit-code families
// the command line offers. The zero value is failOther so an unknown or
// unclassified error falls back to the generic failure exit code.
type startFailure int

const (
	failOther startFailure = iota
	failAlreadyRunning
	failListenUnavailable
	failInvalidConfig
	failInSession
	failNotRoot
	failRuntimeDir
	failInvalidListen
	failSystemUnavailable
)

// Process exit codes. They are split by cause so a service manager and the
// subprocess tests can tell the failures apart.
const (
	exitOK                = 0
	exitFailure           = 1
	exitUsage             = 2
	exitAlreadyRunning    = 3
	exitListenUnavailable = 4
	exitInvalidConfig     = 5
	exitInSession         = 6
	exitNotRoot           = 7
	exitRuntimeDir        = 8
)

// exitCodeFor maps a Broker startup failure to the process exit code. A usage
// problem in the listen address shares the generic usage code; a system-mode
// failure and any unclassified failure use the generic failure code.
func exitCodeFor(f startFailure) int {
	switch f {
	case failAlreadyRunning:
		return exitAlreadyRunning
	case failListenUnavailable:
		return exitListenUnavailable
	case failInvalidConfig:
		return exitInvalidConfig
	case failInSession:
		return exitInSession
	case failNotRoot:
		return exitNotRoot
	case failRuntimeDir:
		return exitRuntimeDir
	case failInvalidListen:
		return exitUsage
	case failOther, failSystemUnavailable:
		return exitFailure
	default:
		return exitFailure
	}
}

// deps holds the upstream capabilities the command line needs. Each field is a
// function so tests can substitute a stub and the wiring point can be checked
// by the compiler.
type deps struct {
	runBroker         func(ctx context.Context, o brokerOptions) error
	classifyBrokerErr func(err error) startFailure
	runMCP            func(ctx context.Context) error // MCP over the process's own stdin/stdout
	runStatus         func(ctx context.Context, stdout io.Writer) error
	runAudit          func(ctx context.Context, stdout io.Writer, limit uint32) error

	// Broker configuration and pairing commands. The Broker is selected the
	// same way status and audit select it; warn carries the warnings of a
	// successful peer connect.
	runConfigGet        func(ctx context.Context, key string, stdout io.Writer) error
	runConfigSet        func(ctx context.Context, key, value string, stdout io.Writer) error
	runPairingKeyCreate func(ctx context.Context, stdout io.Writer) error
	runPeerList         func(ctx context.Context, stdout io.Writer) error
	runPeerConnect      func(ctx context.Context, address, key, alias string, stdout, warn io.Writer) error
	runPeerDisconnect   func(ctx context.Context, broker string, stdout io.Writer) error

	// Update command. The command asks the selected Broker to install the
	// newest release itself. When that Broker is this account's user-level
	// Broker and declines, the command replaces the user-level binary locally.
	updateAPIBase        func() string
	updateLatest         func(ctx context.Context, client *http.Client, api string) (update.Release, error)
	updateNewer          func(current, candidate string) bool
	updateApply          func(ctx context.Context, o update.ApplyOptions) error
	applyUpdateViaBroker func(ctx context.Context) (brokerUpdateResult, error)
	brokerUpdateStatus   func(ctx context.Context) (brokerUpdateStatus, error)
	// restartService is a service operation the update command must not
	// perform. The delegated route leaves restarting to the Broker's service
	// manager, and a local replacement leaves an installed service alone
	// because it may have been stopped on purpose, so nothing calls this. It
	// exists so the test double records an attempt: a change that restarts the
	// service from the command is caught instead of slipping through.
	restartService func(ctx context.Context) error
}

// brokerUpdateResult is the outcome of the Broker's ApplyUpdate RPC as the
// command line needs it. Keeping a local value type lets the command be built
// and tested without the generated local client.
type brokerUpdateResult struct {
	FromVersion string
	ToVersion   string
	Restarting  bool
	// UserLevel reports whether the Broker that answered runs as this
	// account's user-level service. A timeout names the log of that level.
	UserLevel bool
}

// brokerUpdateStatus is the part of UpdateStatus the command reads while it
// waits for a restarted Broker.
type brokerUpdateStatus struct {
	CurrentVersion string
}
