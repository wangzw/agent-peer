package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

const brokerUsage = "usage: agent-peer broker [--system] [--listen <address>] [--allow-degraded]\n"

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
	allowDegraded := fs.Bool("allow-degraded", false, "keep running and report failures on the local socket instead of exiting")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, brokerUsage)
			return exitOK
		}
		return a.brokerUsageError(err.Error())
	}

	var listenSet bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			listenSet = true
		}
	})

	if fs.NArg() > 0 {
		return a.brokerUsageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if listenSet && *listen == "" {
		return a.brokerUsageError("--listen requires a non-empty address; to stop listening, set broker.listen to an empty value in the configuration")
	}

	err := a.deps.runBroker(ctx, brokerOptions{
		System:        *system,
		Listen:        *listen,
		AllowDegraded: *allowDegraded,
		Version:       a.version,
		LogOutput:     a.stderr,
	})
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

// brokerUsageError reports a usage error that did not reach broker.Run.
func (a *app) brokerUsageError(reason string) int {
	_, _ = fmt.Fprint(a.stderr, brokerUsage)
	_, _ = fmt.Fprintf(a.stderr, "agent-peer broker: %s\n", reason)
	return exitUsage
}
