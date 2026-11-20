package main

import (
	"context"
	"fmt"
)

func init() {
	subcommands["offline"] = (*app).offlineCmd
	subcommands["online"] = (*app).onlineCmd
}

const (
	offlineUsage = "usage: agent-peer offline\n"
	onlineUsage  = "usage: agent-peer online\n"
)

// offlineCmd implements "agent-peer offline": it makes the running Broker
// refuse messages from other hosts while keeping its links.
func (a *app) offlineCmd(ctx context.Context, args []string) int {
	return a.setOnlineCmd(ctx, args, false, offlineUsage, "offline")
}

// onlineCmd implements "agent-peer online": it makes the running Broker accept
// messages from other hosts again.
func (a *app) onlineCmd(ctx context.Context, args []string) int {
	return a.setOnlineCmd(ctx, args, true, onlineUsage, "online")
}

// setOnlineCmd is the shared body of offline and online. Both take no
// arguments and report a usage error for anything after the command name.
func (a *app) setOnlineCmd(ctx context.Context, args []string, online bool, usage, command string) int {
	fs := noFlagSet(command)
	if code, ok := a.terminalFlags(fs, usage, command, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return a.terminalUsageError(usage, command, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	if err := a.deps.runSetOnline(ctx, online, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer %s: %v\n", command, err)
		return exitFailure
	}
	return exitOK
}
