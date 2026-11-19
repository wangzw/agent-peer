package main

import (
	"context"
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
  service install [--system]  install and start the Broker as a user-level service,
                              or system-level with --system
  service uninstall [--system]
                              stop and remove the installed service; configuration,
                              identity and audit logs are kept
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

// signalRestoreHook runs after the first signal has cancelled ctx and the
// default signal handling has been restored. Production builds keep the no-op
// value; the agentpeer_testenv build points it at a channel so a subprocess
// test can observe the restore instead of waiting a fixed interval.
var signalRestoreHook = func() {}

// newSignalContext cancels the returned context on SIGINT or SIGTERM. After the
// first signal restores the default handling, so a second signal terminates the
// process immediately.
func newSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
		signalRestoreHook()
	}()
	return ctx, stop
}

// run dispatches one command line and returns the process exit code.
func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(a.stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, usage)
		return exitOK
	case "--version":
		if len(args) > 1 {
			_, _ = fmt.Fprintln(a.stderr, "agent-peer: --version takes no arguments")
			return exitUsage
		}
		_, _ = fmt.Fprintf(a.stdout, "agent-peer %s\n", a.version)
		return exitOK
	case "internal":
		return a.runInternal(ctx, args[1:])
	}

	name := args[0]
	if handler, ok := subcommands[name]; ok {
		return handler(a, ctx, args[1:])
	}
	_, _ = fmt.Fprintf(a.stderr, "agent-peer: unknown command %q\n\n", name)
	_, _ = fmt.Fprint(a.stderr, usage)
	return exitUsage
}

// runInternal handles the hidden internal helper commands. Their errors never
// print the top-level usage, because the group is not shown to users.
func (a *app) runInternal(ctx context.Context, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(a.stderr, `agent-peer internal: unknown command ""`)
		return exitUsage
	}
	name := "internal " + args[0]
	if handler, ok := subcommands[name]; ok {
		return handler(a, ctx, args[1:])
	}
	_, _ = fmt.Fprintf(a.stderr, "agent-peer internal: unknown command %q\n", args[0])
	return exitUsage
}
