package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/wangzw/peers/internal/broker/helper"
	"github.com/wangzw/peers/internal/broker/service"
)

// The test seams replace the helper entry points so the command tests can check
// the streams and the exit-code mapping without running the pipe protocol.
// postUpdateFn is the service rewrite behind "internal post-update".
var (
	runAccountHelperFn = helper.RunAccountHelper
	runAccessCheckFn   = helper.RunAccessCheck
	postUpdateFn       = service.PostUpdate
)

func init() {
	subcommands["internal account-helper"] = (*app).accountHelperCmd
	subcommands["internal access-check"] = (*app).accessCheckCmd
	subcommands["internal post-update"] = (*app).postUpdateCmd
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

// postUpdateCmd is the body of the hidden "internal post-update" subcommand.
// The freshly installed binary runs it after an update replaced the binary, so
// the service definition is rewritten in that binary's own format. The join
// point is a cross-version contract: the command name and --level values must
// not change, because the previous binary invokes them.
func (a *app) postUpdateCmd(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("internal post-update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	level := fs.String("level", "", "service level to rewrite: system or user")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return a.postUpdateUsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return a.postUpdateUsageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	system, err := postUpdateLevel(*level)
	if err != nil {
		return a.postUpdateUsageError(err.Error())
	}

	opts, err := defaultOptionsFn(system)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", err)
		return exitFailure
	}
	if err := postUpdateFn(opts); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", err)
		return exitFailure
	}
	return exitOK
}

// postUpdateLevel maps the contract's --level value to the service level.
func postUpdateLevel(name string) (bool, error) {
	switch name {
	case "system":
		return true, nil
	case "user":
		return false, nil
	case "":
		return false, errors.New("missing --level; want system or user")
	default:
		return false, fmt.Errorf("invalid --level %q; want system or user", name)
	}
}

// postUpdateUsageError reports a usage problem without printing the top-level
// usage: the whole internal group stays hidden.
func (a *app) postUpdateUsageError(reason string) int {
	_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", reason)
	return exitUsage
}
