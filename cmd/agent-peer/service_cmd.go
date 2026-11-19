package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/wangzw/agent-peer/internal/broker/service"
)

const serviceUsage = "usage: agent-peer service install [--system]\n"

// The test seams replace the installation steps so the command tests can check
// the option mapping and the exit-code mapping without touching the system.
var (
	defaultOptionsFn = service.DefaultOptions
	installFn        = service.Install
)

func init() {
	subcommands["service"] = (*app).serviceCmd
}

// serviceCmd implements "agent-peer service install [--system]". The
// installation enables the service and starts it immediately.
func (a *app) serviceCmd(_ context.Context, args []string) int {
	if len(args) == 0 {
		return a.serviceUsageError("service", "missing command")
	}

	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, serviceUsage)
		return exitOK
	case "install":
	default:
		return a.serviceUsageError("service", fmt.Sprintf("unknown command %q", args[0]))
	}

	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	system := fs.Bool("system", false, "install the system-level service")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, serviceUsage)
			return exitOK
		}
		return a.serviceUsageError("service install", err.Error())
	}
	if fs.NArg() > 0 {
		return a.serviceUsageError("service install", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	opts, err := defaultOptionsFn(*system)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer service install: %s\n", err)
		return exitFailure
	}
	result, err := installFn(opts)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer service install: %s\n", err)
		return exitFailure
	}
	a.printInstalled(result)
	return exitOK
}

// printInstalled reports that the service is installed and running.
func (a *app) printInstalled(result service.Result) {
	if result.Domain != "" {
		_, _ = fmt.Fprintf(a.stdout, "installed and started %s in %s\n", service.Label, result.Domain)
		return
	}
	_, _ = fmt.Fprintf(a.stdout, "installed and started %s\n", service.UnitName)
}

// serviceUsageError reports a usage error found before the operation starts.
// It prints the subcommand's usage, not the top-level one.
func (a *app) serviceUsageError(command, reason string) int {
	_, _ = fmt.Fprint(a.stderr, serviceUsage)
	_, _ = fmt.Fprintf(a.stderr, "agent-peer %s: %s\n", command, reason)
	return exitUsage
}
