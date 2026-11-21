package main

import (
	"context"
	"fmt"

	"github.com/wangzw/peers/internal/broker/helper"
)

// The test seams replace the helper entry points so the command tests can check
// the streams and the exit-code mapping without running the pipe protocol.
var (
	runAccountHelperFn = helper.RunAccountHelper
	runAccessCheckFn   = helper.RunAccessCheck
)

func init() {
	subcommands["internal account-helper"] = (*app).accountHelperCmd
	subcommands["internal access-check"] = (*app).accessCheckCmd
}

// accountHelperCmd is the body of the hidden "internal account-helper"
// subcommand. The system Broker launches it as an account it serves, and it
// speaks the helper protocol over the process's own standard streams.
func (a *app) accountHelperCmd(ctx context.Context, args []string) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(a.stderr, "peers internal account-helper: unexpected argument %q\n", args[0])
		return exitUsage
	}
	if err := runAccountHelperFn(ctx, a.stdin, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal account-helper: %s\n", err)
		return exitFailure
	}
	return exitOK
}

// accessCheckCmd is the body of the hidden "internal access-check" subcommand.
// The system Broker launches it as the calling account; it reads one request
// from standard input, writes one response to standard output and exits.
func (a *app) accessCheckCmd(_ context.Context, args []string) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(a.stderr, "peers internal access-check: unexpected argument %q\n", args[0])
		return exitUsage
	}
	if err := runAccessCheckFn(a.stdin, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal access-check: %s\n", err)
		return exitFailure
	}
	return exitOK
}
