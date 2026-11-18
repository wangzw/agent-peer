package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/wangzw/agent-peer/internal/broker/service"
)

const serviceInstallUsage = "usage: agent-peer service install [--system]\n"

// The test seams replace the installation steps so the command tests can check
// the option mapping and the exit-code mapping without touching the system.
var (
	defaultOptionsFn = service.DefaultOptions
	installFn        = service.Install
)

func init() {
	subcommands["service"] = (*app).serviceCmd
}

// serviceCmd implements "agent-peer service install [--system]". It installs
// and enables the service but does not start it; the last line on standard
// output is the command that starts it.
func (a *app) serviceCmd(_ context.Context, args []string) int {
	if len(args) == 0 {
		return a.serviceUsageError("service", "missing command")
	}

	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, serviceInstallUsage)
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
			_, _ = fmt.Fprint(a.stdout, serviceInstallUsage)
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
	if result.StartHint != "" {
		_, _ = fmt.Fprintln(a.stdout, result.StartHint)
	}
	return exitOK
}

// serviceUsageError reports a usage error found before the installation starts.
// It prints the subcommand's usage, not the top-level one.
func (a *app) serviceUsageError(command, reason string) int {
	_, _ = fmt.Fprint(a.stderr, serviceInstallUsage)
	_, _ = fmt.Fprintf(a.stderr, "agent-peer %s: %s\n", command, reason)
	return exitUsage
}
