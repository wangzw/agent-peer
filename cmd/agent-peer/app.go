package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage: agent-peer <command> [arguments]

Commands:
  --version                   print the version and exit
  broker [--system] [--listen <address>]
                              run the Broker
  mcp                         run the MCP server for one Claude Code session
  status                      show the state of a running Broker
  audit [--limit <n>]         read the audit log
  service install [--system]  install or manage the system-level Broker service
`

// app holds the process's standard streams, the version to report, and the
// upstream operations the subcommands dispatch to.
type app struct {
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	version string
	deps    deps
}

// subcommands maps a command name to its handler. Command implementations
// register themselves from init so this file stays free of their details.
// The hidden internal helper commands use keys such as "internal broker-launcher".
var subcommands = map[string]func(*app, context.Context, []string) int{}

var errNotWired = errors.New("not implemented")

// realDeps is the temporary assembly used before the real Broker and MCP server
// entry points are wired. Every operation reports that it is not implemented.
// The wiring change replaces this function with one built from the real
// packages.
func realDeps(string) deps {
	return deps{
		runBroker:         func(context.Context, brokerOptions) error { return errNotWired },
		classifyBrokerErr: func(error) startFailure { return failOther },
		runMCP:            func(context.Context) error { return errNotWired },
		runLauncher:       func() int { return exitFailure },
		runStatus:         func(context.Context, io.Writer) error { return errNotWired },
		runAudit:          func(context.Context, io.Writer, uint32) error { return errNotWired },
	}
}

// newSignalContext cancels the returned context on SIGINT or SIGTERM. After the
// first signal restores the default handling, so a second signal terminates the
// process immediately.
func newSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// run dispatches one command line and returns the process exit code.
func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(a.stdout, usage)
		return exitOK
	case "--version":
		if len(args) > 1 {
			fmt.Fprintln(a.stderr, "agent-peer: --version takes no arguments")
			return exitUsage
		}
		fmt.Fprintf(a.stdout, "agent-peer %s\n", a.version)
		return exitOK
	case "service":
		fmt.Fprintln(a.stderr, "agent-peer service: not implemented")
		return exitFailure
	case "internal":
		return a.runInternal(ctx, args[1:])
	}

	name := args[0]
	if handler, ok := subcommands[name]; ok {
		return handler(a, ctx, args[1:])
	}
	switch name {
	case "broker", "mcp", "status", "audit":
		fmt.Fprintf(a.stderr, "agent-peer %s: not implemented\n", name)
		return exitFailure
	}
	fmt.Fprintf(a.stderr, "agent-peer: unknown command %q\n\n", name)
	fmt.Fprint(a.stderr, usage)
	return exitUsage
}

// runInternal handles the hidden internal helper commands. Their errors never
// print the top-level usage, because the group is not shown to users.
func (a *app) runInternal(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, `agent-peer internal: unknown command ""`)
		return exitUsage
	}
	name := "internal " + args[0]
	if handler, ok := subcommands[name]; ok {
		return handler(a, ctx, args[1:])
	}
	switch args[0] {
	case "broker-launcher", "account-helper", "access-check":
		fmt.Fprintf(a.stderr, "agent-peer %s: not implemented\n", name)
		return exitFailure
	}
	fmt.Fprintf(a.stderr, "agent-peer internal: unknown command %q\n", args[0])
	return exitUsage
}
