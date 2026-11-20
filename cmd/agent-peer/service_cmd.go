package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wangzw/agent-peer/internal/broker/service"
)

const serviceUsage = "usage: agent-peer service install [--system] [--admin-group <name>]\n" +
	"       agent-peer service uninstall [--system]\n" +
	"       agent-peer service start|stop|restart\n"

// The test seams replace the installation steps and the account check so the
// command tests can check the option mapping and the exit-code mapping without
// touching the system.
var (
	defaultOptionsFn = service.DefaultOptions
	installFn        = service.Install
	uninstallFn      = service.Uninstall
	startFn          = service.Start
	stopFn           = service.Stop
	restartFn        = service.Restart
	installedFn      = service.Installed
	serviceEUID      = os.Geteuid
)

func init() {
	subcommands["service"] = (*app).serviceCmd
}

// serviceCmd implements the service subcommands. install enables the service
// and starts it immediately; start, stop and restart act on the service of the
// executing account and take no arguments.
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
		return a.serviceInstallCmd(command, args[1:])
	case "start", "stop", "restart":
		return a.serviceControlCmd(command, args[1:])
	default:
		return a.serviceUsageError("service", fmt.Sprintf("unknown command %q", command))
	}
}

// serviceInstallCmd implements "service install [--system]" and
// "service uninstall [--system]".
func (a *app) serviceInstallCmd(command string, args []string) int {
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
	if err := fs.Parse(args); err != nil {
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

// serviceControlCmd implements "service start|stop|restart". The level follows
// the executing account: root acts on the system-level service, every other
// account on its own user-level service. The commands do not accept --system.
func (a *app) serviceControlCmd(command string, args []string) int {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			_, _ = fmt.Fprint(a.stdout, serviceUsage)
			return exitOK
		}
	}
	if len(args) > 0 {
		if args[0] == "--system" || strings.HasPrefix(args[0], "--system=") {
			return a.serviceUsageError("service "+command,
				"--system is not accepted; start, stop and restart act on the level of the executing account (root: system-level, other accounts: user-level)")
		}
		return a.serviceUsageError("service "+command, fmt.Sprintf("unexpected argument %q", args[0]))
	}

	opts, err := defaultOptionsFn(serviceEUID() == 0)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer service %s: %s\n", command, err)
		return exitFailure
	}

	var operation func(service.Options) (service.Result, error)
	switch command {
	case "start":
		operation = startFn
	case "stop":
		operation = stopFn
	case "restart":
		operation = restartFn
	}
	_, err = operation(opts)
	if err == nil {
		a.printServiceAction(command, opts.System)
		return exitOK
	}

	var degraded *service.DegradedError
	if errors.As(err, &degraded) {
		a.printServiceAction(command, opts.System)
		a.printServiceDegraded(degraded.State)
		return exitServiceDegraded
	}
	if errors.Is(err, service.ErrNotInstalled) {
		return a.serviceNotInstalled(command, opts.System)
	}
	_, _ = fmt.Fprintf(a.stderr, "agent-peer service %s: %s\n", command, err)
	return exitFailure
}

// serviceNotInstalled explains that the command's level has no service. A
// non-root account that only has the system-level install is pointed at the
// sudo form of the same command instead of installing anything.
func (a *app) serviceNotInstalled(command string, system bool) int {
	if !system && serviceEUID() != 0 {
		systemOpts, err := defaultOptionsFn(true)
		if err == nil {
			if installed, instErr := installedFn(systemOpts); instErr == nil && installed {
				_, _ = fmt.Fprintf(a.stderr,
					"agent-peer service %s: no user-level service is installed; the system-level service is installed, run `sudo agent-peer service %s`\n",
					command, command)
				return exitFailure
			}
		}
	}

	level, install := "user-level", "agent-peer service install"
	if system {
		level, install = "system-level", "sudo agent-peer service install --system"
	}
	_, _ = fmt.Fprintf(a.stderr,
		"agent-peer service %s: no %s service is installed; install it with `%s`\n",
		command, level, install)
	return exitFailure
}

// printServiceAction reports the one-line result of a successful command.
func (a *app) printServiceAction(command string, system bool) {
	past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[command]
	level := "user-level"
	if system {
		level = "system-level"
	}
	_, _ = fmt.Fprintf(a.stdout, "Broker service %s (%s)\n", past, level)
}

// printServiceDegraded prints the two status lines the service commands share
// with the terminal's degraded display.
func (a *app) printServiceDegraded(state service.DegradedState) {
	line := "Broker: degraded"
	if state.Level != "" {
		line += " (" + strings.ReplaceAll(state.Level, "_", " ") + ")"
	}
	line += ": " + state.Reason
	_, _ = fmt.Fprintln(a.stderr, line)
	_, _ = fmt.Fprintf(a.stderr, "Fix: %s\n", state.Fix)
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
