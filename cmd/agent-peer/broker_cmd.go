package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
)

// readyFDFlagName is the hidden flag that carries the file descriptor of the
// readiness pipe handed down by the MCP server. It matches mcpserver.ReadyFDFlag
// without the leading dashes.
const readyFDFlagName = "ready-fd"

const brokerUsage = "usage: agent-peer broker [--system] [--listen <address>]\n"

func init() {
	subcommands["broker"] = (*app).brokerCmd
}

// brokerCmd parses the broker subcommand's arguments and runs the Broker. It
// translates startup failures into exit codes and reports them on standard
// error.
func (a *app) brokerCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("broker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	system := fs.Bool("system", false, "run the system-level Broker")
	listen := fs.String("listen", "", "listen address, overriding broker.listen")
	readyFD := fs.Int(readyFDFlagName, -1, "file descriptor of the readiness pipe")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, brokerUsage)
			return exitOK
		}
		return a.brokerUsageError(err.Error())
	}

	var (
		listenSet  bool
		readyFDSet bool
	)
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen":
			listenSet = true
		case readyFDFlagName:
			readyFDSet = true
		}
	})

	var ready *os.File
	if readyFDSet {
		fd := *readyFD
		if fd >= 0 {
			if fd <= 2 {
				return a.brokerUsageError(fmt.Sprintf("--ready-fd %d names a standard stream; the readiness pipe is passed at a descriptor of 3 or higher", fd))
			}
			var st syscall.Stat_t
			if err := syscall.Fstat(fd, &st); err != nil {
				return a.brokerUsageError(fmt.Sprintf("--ready-fd %d is not an open file descriptor", fd))
			}
			ready = os.NewFile(uintptr(fd), "ready")
		} else if fd != -1 {
			return a.brokerUsageError(fmt.Sprintf("--ready-fd %d is not an open file descriptor", fd))
		}
	}

	if fs.NArg() > 0 {
		return a.brokerUsageErrorBeforeRun(ready, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if listenSet && *listen == "" {
		return a.brokerUsageErrorBeforeRun(ready, "--listen requires a non-empty address; to stop listening, set broker.listen to an empty value in the configuration")
	}

	err := a.deps.runBroker(ctx, brokerOptions{
		System:    *system,
		Listen:    *listen,
		Ready:     ready,
		Version:   a.version,
		LogOutput: a.stderr,
	})

	// The Broker owns the pipe while it runs; closing it here guarantees that
	// the MCP server sees EOF even when the Broker returned without writing.
	// Closing an already closed file is expected.
	if ready != nil {
		_ = ready.Close()
	}

	if err == nil {
		return exitOK
	}

	failure := a.deps.classifyBrokerErr(err)
	if failure == failInvalidListen {
		_, _ = fmt.Fprint(a.stderr, brokerUsage)
	}
	_, _ = fmt.Fprintf(a.stderr, "agent-peer broker: %s\n", err)
	return exitCodeFor(failure)
}

// brokerUsageError reports a usage error that did not reach broker.Run. When a
// readiness pipe was opened it is closed and nothing is written to it.
func (a *app) brokerUsageError(reason string) int {
	_, _ = fmt.Fprint(a.stderr, brokerUsage)
	_, _ = fmt.Fprintf(a.stderr, "agent-peer broker: %s\n", reason)
	return exitUsage
}

// brokerUsageErrorBeforeRun reports a usage error found after the readiness
// pipe was opened. The reason goes to the pipe first so the MCP server does not
// wait for the full timeout, then to standard error.
func (a *app) brokerUsageErrorBeforeRun(ready *os.File, reason string) int {
	if ready != nil {
		_, _ = fmt.Fprintf(ready, "%s\n", reason)
		_ = ready.Close()
	}
	return a.brokerUsageError(reason)
}
