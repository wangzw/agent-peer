package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

func init() {
	subcommands["config"] = (*app).configCmd
}

const configUsage = "usage: peers config get [<key>]\n" +
	"       peers config set <key> <value>\n"

// terminalUsageError prints a subcommand's usage followed by the reason and
// returns exitUsage, the contract every terminal command shares with status
// and audit.
func (a *app) terminalUsageError(usage, command, reason string) int {
	_, _ = fmt.Fprint(a.stderr, usage)
	_, _ = fmt.Fprintf(a.stderr, "peers %s: %s\n", command, reason)
	return exitUsage
}

// terminalFlags parses the flags of one terminal verb. A help request prints
// the usage to standard output and succeeds; a parse error prints the usage and
// the reason to standard error and returns exitUsage. The bool reports whether
// parsing succeeded; when it is false the caller returns code.
func (a *app) terminalFlags(fs *flag.FlagSet, usage, command string, args []string) (code int, ok bool) {
	// The flag package stops parsing at the first positional argument, so a
	// help request after one would be read as a positional. Scan the whole
	// command line first so -h and --help work in any position.
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			_, _ = fmt.Fprint(a.stdout, usage)
			return exitOK, false
		}
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, usage)
			return exitOK, false
		}
		return a.terminalUsageError(usage, command, err.Error()), false
	}
	return exitOK, true
}

// noFlagSet returns a flag set for a verb that defines no flags of its own. It
// still recognizes -h and --help.
func noFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// configCmd implements "peers config get [<key>]" and
// "peers config set <key> <value>".
func (a *app) configCmd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.terminalUsageError(configUsage, "config", "missing command")
	}
	switch verb := args[0]; verb {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, configUsage)
		return exitOK
	case "get":
		return a.configGetCmd(ctx, args[1:])
	case "set":
		return a.configSetCmd(ctx, args[1:])
	default:
		return a.terminalUsageError(configUsage, "config", fmt.Sprintf("unknown command %q", verb))
	}
}

// configGetCmd implements "peers config get [<key>]". Without a key it
// lists every configuration item.
func (a *app) configGetCmd(ctx context.Context, args []string) int {
	fs := noFlagSet("config get")
	if code, ok := a.terminalFlags(fs, configUsage, "config get", args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		return a.terminalUsageError(configUsage, "config get", fmt.Sprintf("unexpected argument %q", fs.Arg(1)))
	}
	key := ""
	if fs.NArg() == 1 {
		key = fs.Arg(0)
	}

	if err := a.deps.runConfigGet(ctx, key, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers config get: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// configSetCmd implements "peers config set <key> <value>".
func (a *app) configSetCmd(ctx context.Context, args []string) int {
	fs := noFlagSet("config set")
	if code, ok := a.terminalFlags(fs, configUsage, "config set", args); !ok {
		return code
	}
	switch {
	case fs.NArg() < 2:
		return a.terminalUsageError(configUsage, "config set", "expected <key> <value>")
	case fs.NArg() > 2:
		return a.terminalUsageError(configUsage, "config set", fmt.Sprintf("unexpected argument %q", fs.Arg(2)))
	}

	if err := a.deps.runConfigSet(ctx, fs.Arg(0), fs.Arg(1), a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers config set: %v\n", err)
		return exitFailure
	}
	return exitOK
}
