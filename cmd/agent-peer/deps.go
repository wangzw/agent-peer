package main

import (
	"context"
	"io"
	"os"
)

// brokerOptions is the subset of the Broker's run options that the command
// line controls. It keeps cmd independent of the broker package so that the
// subcommands can be built and tested before the Broker's entry point exists.
type brokerOptions struct {
	System    bool
	Listen    string
	Ready     *os.File // nil means there is no ready pipe
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
	runLauncher       func() int                      // body of "internal broker-launcher", returns the exit code
	runStatus         func(ctx context.Context, stdout io.Writer) error
	runAudit          func(ctx context.Context, stdout io.Writer, limit uint32) error
}
