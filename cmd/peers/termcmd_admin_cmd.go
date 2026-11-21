package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func init() {
	subcommands["config"] = (*app).configCmd
	subcommands["pairing-key"] = (*app).pairingKeyCmd
	subcommands["peer"] = (*app).peerCmd
}

const (
	configUsage = "usage: peers config get [<key>]\n" +
		"       peers config set <key> <value>\n"
	pairingKeyUsage = "usage: peers pairing-key create\n"
	peerUsage       = "usage: peers peer list\n" +
		"       peers peer connect <address> --key <key> [--alias <alias>]\n" +
		"       peers peer disconnect <broker>\n"
)

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

// pairingKeyCmd implements "peers pairing-key create".
func (a *app) pairingKeyCmd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.terminalUsageError(pairingKeyUsage, "pairing-key", "missing command")
	}
	switch verb := args[0]; verb {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, pairingKeyUsage)
		return exitOK
	case "create":
	default:
		return a.terminalUsageError(pairingKeyUsage, "pairing-key", fmt.Sprintf("unknown command %q", verb))
	}

	fs := noFlagSet("pairing-key create")
	if code, ok := a.terminalFlags(fs, pairingKeyUsage, "pairing-key create", args[1:]); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return a.terminalUsageError(pairingKeyUsage, "pairing-key create", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	if err := a.deps.runPairingKeyCreate(ctx, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers pairing-key create: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// peerCmd implements "peers peer list", "peers peer connect" and
// "peers peer disconnect".
func (a *app) peerCmd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.terminalUsageError(peerUsage, "peer", "missing command")
	}
	switch verb := args[0]; verb {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.stdout, peerUsage)
		return exitOK
	case "list":
		return a.peerListCmd(ctx, args[1:])
	case "connect":
		return a.peerConnectCmd(ctx, args[1:])
	case "disconnect":
		return a.peerDisconnectCmd(ctx, args[1:])
	default:
		return a.terminalUsageError(peerUsage, "peer", fmt.Sprintf("unknown command %q", verb))
	}
}

// peerListCmd implements "peers peer list".
func (a *app) peerListCmd(ctx context.Context, args []string) int {
	fs := noFlagSet("peer list")
	if code, ok := a.terminalFlags(fs, peerUsage, "peer list", args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return a.terminalUsageError(peerUsage, "peer list", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	if err := a.deps.runPeerList(ctx, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers peer list: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// peerConnectArgs holds one parsed "peer connect" command line.
type peerConnectArgs struct {
	address string
	key     string
	alias   string
}

// parsePeerConnectArgs parses "peer connect <address> --key <key> [--alias
// <alias>]". The grammar allows the address before or after the flags, which
// the flag package cannot parse because it stops at the first non-flag
// argument, so the arguments are read in one pass here.
func parsePeerConnectArgs(args []string) (peerConnectArgs, bool, error) {
	var (
		parsed     peerConnectArgs
		positional int
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			return peerConnectArgs{}, true, nil
		case arg == "--key" || arg == "-key":
			if i+1 >= len(args) {
				return peerConnectArgs{}, false, errors.New("flag needs an argument: -key")
			}
			i++
			parsed.key = args[i]
		case strings.HasPrefix(arg, "--key="):
			parsed.key = strings.TrimPrefix(arg, "--key=")
		case strings.HasPrefix(arg, "-key="):
			parsed.key = strings.TrimPrefix(arg, "-key=")
		case arg == "--alias" || arg == "-alias":
			if i+1 >= len(args) {
				return peerConnectArgs{}, false, errors.New("flag needs an argument: -alias")
			}
			i++
			parsed.alias = args[i]
		case strings.HasPrefix(arg, "--alias="):
			parsed.alias = strings.TrimPrefix(arg, "--alias=")
		case strings.HasPrefix(arg, "-alias="):
			parsed.alias = strings.TrimPrefix(arg, "-alias=")
		case strings.HasPrefix(arg, "-") && arg != "-":
			return peerConnectArgs{}, false, fmt.Errorf("flag provided but not defined: -%s", strings.TrimLeft(arg, "-"))
		default:
			// Never repeat a positional argument: the second one is usually a
			// pairing key whose --key flag was left out.
			positional++
			if positional > 1 {
				return peerConnectArgs{}, false, fmt.Errorf("unexpected positional argument at position %d", positional)
			}
			parsed.address = arg
		}
	}
	return parsed, false, nil
}

// peerConnectCmd implements "peers peer connect <address> --key <key>
// [--alias <alias>]". Warnings from the Broker are written to standard error
// by the termcmd call and do not change the exit code.
func (a *app) peerConnectCmd(ctx context.Context, args []string) int {
	parsed, help, err := parsePeerConnectArgs(args)
	if help {
		_, _ = fmt.Fprint(a.stdout, peerUsage)
		return exitOK
	}
	if err != nil {
		return a.terminalUsageError(peerUsage, "peer connect", err.Error())
	}
	if parsed.address == "" {
		return a.terminalUsageError(peerUsage, "peer connect", "expected <address>")
	}
	if parsed.key == "" {
		return a.terminalUsageError(peerUsage, "peer connect", "missing --key")
	}

	if err := a.deps.runPeerConnect(ctx, parsed.address, parsed.key, parsed.alias, a.stdout, a.stderr); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers peer connect: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// peerDisconnectCmd implements "peers peer disconnect <broker>".
func (a *app) peerDisconnectCmd(ctx context.Context, args []string) int {
	fs := noFlagSet("peer disconnect")
	if code, ok := a.terminalFlags(fs, peerUsage, "peer disconnect", args); !ok {
		return code
	}
	switch {
	case fs.NArg() == 0:
		return a.terminalUsageError(peerUsage, "peer disconnect", "expected <broker>")
	case fs.NArg() > 1:
		return a.terminalUsageError(peerUsage, "peer disconnect", fmt.Sprintf("unexpected argument %q", fs.Arg(1)))
	}

	if err := a.deps.runPeerDisconnect(ctx, fs.Arg(0), a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers peer disconnect: %v\n", err)
		return exitFailure
	}
	return exitOK
}
