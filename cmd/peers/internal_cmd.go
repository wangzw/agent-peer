package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wangzw/peers/internal/broker/helper"
	"github.com/wangzw/peers/internal/broker/service"
)

// The test seams replace the helper entry points so the command tests can check
// the streams and the exit-code mapping without running the pipe protocol.
// postUpdateOptionsFn builds the options for the level named by --level, and
// postUpdateFn is the service rewrite behind "internal post-update".
var (
	runAccountHelperFn  = helper.RunAccountHelper
	runAccessCheckFn    = helper.RunAccessCheck
	postUpdateOptionsFn = service.PostUpdateOptions
	postUpdateFn        = service.PostUpdate
	relaunchOptionsFn   = service.RelaunchOptions
	launchdReloadFn     = service.LaunchdReload
)

func init() {
	subcommands["internal account-helper"] = (*app).accountHelperCmd
	subcommands["internal access-check"] = (*app).accessCheckCmd
	subcommands["internal post-update"] = (*app).postUpdateCmd
	subcommands["internal launchd-reload"] = (*app).launchdReloadCmd
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
	pidText := fs.String("pid", "", "pid of the Broker that answered the update; optional")
	if err := fs.Parse(postUpdateKnownArgs(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return a.postUpdateError(err.Error())
	}
	if fs.NArg() > 0 {
		return a.postUpdateError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	system, err := postUpdateLevel(*level)
	if err != nil {
		return a.postUpdateError(err.Error())
	}

	opts, err := postUpdateOptionsFn(system)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", err)
		return exitFailure
	}
	if *pidText != "" {
		pid, err := strconv.Atoi(*pidText)
		if err != nil || pid <= 0 {
			return a.postUpdateError(fmt.Sprintf("invalid --pid %q", *pidText))
		}
		opts.PPID = func() int { return pid }
	}
	if err := postUpdateFn(opts); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", err)
		return exitFailure
	}
	return exitOK
}

// postUpdateKnownArgs keeps the options this version understands and drops any
// other option. A later release may add optional parameters to the call, and a
// binary that does not know one must still run the step instead of failing on
// it. An option carried as a separate value ("--name value") cannot be told
// apart from an unexpected positional argument once its name is dropped, so a
// parameter this version does not know has to use the "--name=value" form.
func postUpdateKnownArgs(args []string) []string {
	kept := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			kept = append(kept, arg)
			continue
		}
		name := arg
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		switch name {
		case "--level", "-level", "--pid", "-pid", "--help", "-help", "-h", "--":
			kept = append(kept, arg)
		}
	}
	return kept
}

// launchdReloadCmd is the body of the hidden "internal launchd-reload"
// subcommand. On macOS the post-update step starts it in a detached session;
// it waits for the Broker that answered the update to exit, then boots the
// label out and bootstraps the rewritten plist. It is internal implementation,
// not part of the cross-version contract: the contract is the post-update
// command that starts it.
func (a *app) launchdReloadCmd(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("internal launchd-reload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	level := fs.String("level", "", "service level: system or user")
	domain := fs.String("domain", "", "launchd domain")
	plist := fs.String("plist", "", "path of the rewritten plist")
	pidText := fs.String("pid", "", "pid of the Broker that answered the update")
	exeDev := fs.String("exe-dev", "", "device of the binary that started the helper")
	exeIno := fs.String("exe-ino", "", "inode of the binary that started the helper")
	audit := fs.String("audit", "", "path of the level's audit log")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return a.launchdReloadUsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return a.launchdReloadUsageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	system, err := postUpdateLevel(*level)
	if err != nil {
		return a.launchdReloadUsageError(err.Error())
	}
	if *domain == "" {
		return a.launchdReloadUsageError("missing --domain")
	}
	if !filepath.IsAbs(*plist) {
		return a.launchdReloadUsageError(fmt.Sprintf("--plist %q must be an absolute path", *plist))
	}
	pid, err := strconv.Atoi(*pidText)
	if err != nil || pid <= 0 {
		return a.launchdReloadUsageError(fmt.Sprintf("invalid --pid %q", *pidText))
	}
	dev, err := strconv.ParseUint(*exeDev, 10, 64)
	if err != nil {
		return a.launchdReloadUsageError(fmt.Sprintf("invalid --exe-dev %q", *exeDev))
	}
	ino, err := strconv.ParseUint(*exeIno, 10, 64)
	if err != nil {
		return a.launchdReloadUsageError(fmt.Sprintf("invalid --exe-ino %q", *exeIno))
	}

	opts := relaunchOptionsFn(system)
	if err := launchdReloadFn(opts, *domain, *plist, pid, service.ExecutableID{Dev: dev, Ino: ino}, *audit); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "peers internal launchd-reload: %s\n", err)
		return exitFailure
	}
	return exitOK
}

// launchdReloadUsageError reports a usage problem without printing the
// top-level usage: the whole internal group stays hidden.
func (a *app) launchdReloadUsageError(reason string) int {
	_, _ = fmt.Fprintf(a.stderr, "peers internal launchd-reload: %s\n", reason)
	return exitUsage
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

// postUpdateError reports a problem with the post-update call and returns the
// general failure code. Exit code 2 is reserved for "this binary has no such
// subcommand": the previous binary reads it as "no post-update step" and
// continues the update. A usage error of this command must not be mistaken for
// that, so it is a failure. The top-level usage is not printed: the whole
// internal group stays hidden.
func (a *app) postUpdateError(reason string) int {
	_, _ = fmt.Fprintf(a.stderr, "peers internal post-update: %s\n", reason)
	return exitFailure
}
