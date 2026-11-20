package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/wangzw/agent-peer/internal/broker/service"
)

const serviceUsage = "usage: agent-peer service install [--system] [--admin-group <name>]\n" +
	"       agent-peer service uninstall [--system]\n"

// The test seams replace the installation steps so the command tests can check
// the option mapping and the exit-code mapping without touching the system.
var (
	defaultOptionsFn = service.DefaultOptions
	installFn        = service.Install
	uninstallFn      = service.Uninstall
)

func init() {
	subcommands["service"] = (*app).serviceCmd
}

// serviceCmd implements "agent-peer service install [--system]" and
// "agent-peer service uninstall [--system]". install enables the service and
// starts it immediately.
func (a *app) serviceCmd(_ context.Context, args []string) int {
	if len(args) == 0 {
		return a.serviceUsageError("service", "missing command")
	}

	command := args[0]
	switch command {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, serviceUsage)
		return exitOK
	case "install", "uninstall":
	default:
		return a.serviceUsageError("service", fmt.Sprintf("unknown command %q", command))
	}

	fs := flag.NewFlagSet("service "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	system := fs.Bool("system", false, "use the system-level service")
	var (
		adminGroup    string
		adminGroupSet bool
	)
	if command == "install" {
		fs.StringVar(&adminGroup, "admin-group", service.DefaultAdminGroup, "group that administers a system-level Broker")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, serviceUsage)
			return exitOK
		}
		return a.serviceUsageError("service "+command, err.Error())
	}
	if fs.NArg() > 0 {
		return a.serviceUsageError("service "+command, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if command == "install" {
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "admin-group" {
				adminGroupSet = true
			}
		})
		if adminGroupSet && !*system {
			return a.serviceUsageError("service install", "--admin-group requires --system")
		}
		if !service.ValidAdminGroupName(adminGroup) {
			return a.serviceUsageError("service install", fmt.Sprintf(
				"invalid --admin-group %q: a group name starts with a lowercase letter or underscore and contains only lowercase letters, digits, underscores and hyphens", adminGroup))
		}
	}

	opts, err := defaultOptionsFn(*system)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer service %s: %s\n", command, err)
		return exitFailure
	}
	if command == "install" {
		opts.AdminGroup = adminGroup
		result, err := installFn(opts)
		if err != nil {
			_, _ = fmt.Fprintf(a.stderr, "agent-peer service install: %s\n", err)
			return exitFailure
		}
		a.printInstalled(result)
		return exitOK
	}
	result, err := uninstallFn(opts)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer service uninstall: %s\n", err)
		return exitFailure
	}
	a.printUninstalled(result)
	return exitOK
}

// printInstalled reports that the service is installed and running and what
// happened to the configuration file.
func (a *app) printInstalled(result service.Result) {
	if result.Domain != "" {
		_, _ = fmt.Fprintf(a.stdout, "installed and started %s in %s\n", service.Label, result.Domain)
	} else {
		_, _ = fmt.Fprintf(a.stdout, "installed and started %s\n", service.UnitName)
	}
	switch {
	case result.ConfigPath == "":
		return
	case result.ConfigNew:
		_, _ = fmt.Fprintf(a.stdout, "generated the configuration file %s\n", result.ConfigPath)
	default:
		_, _ = fmt.Fprintf(a.stdout, "kept the existing configuration file %s\n", result.ConfigPath)
		if result.AdminGroup != "" {
			_, _ = fmt.Fprintf(a.stdout, "To delegate administration, run: chgrp %s %s && chmod 0660 %s\n",
				result.AdminGroup, result.ConfigPath, result.ConfigPath)
		}
	}
}

// printUninstalled reports what uninstall removed, or that nothing was
// installed.
func (a *app) printUninstalled(result service.Result) {
	if !result.Removed {
		_, _ = fmt.Fprintln(a.stdout, "agent-peer service is not installed")
		return
	}
	if result.Domain != "" {
		_, _ = fmt.Fprintf(a.stdout, "uninstalled %s from %s\n", service.Label, result.Domain)
		return
	}
	_, _ = fmt.Fprintf(a.stdout, "uninstalled %s\n", service.UnitName)
}

// serviceUsageError reports a usage error found before the operation starts.
// It prints the subcommand's usage, not the top-level one.
func (a *app) serviceUsageError(command, reason string) int {
	_, _ = fmt.Fprint(a.stderr, serviceUsage)
	_, _ = fmt.Fprintf(a.stderr, "agent-peer %s: %s\n", command, reason)
	return exitUsage
}
