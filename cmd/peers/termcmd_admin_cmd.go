package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// terminalUsageError prints a subcommand's usage followed by the reason and
// returns exitUsage, the contract the legacy terminal commands share.
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
