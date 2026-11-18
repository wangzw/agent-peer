//go:build agentpeer_testenv

package main

import (
	"context"
	"fmt"
	"time"
)

// signal-block is a hidden subcommand that exists only in test builds. A
// subprocess test uses it to observe the process-level signal contract: the
// first SIGINT or SIGTERM cancels ctx, and the second terminates the process.
// It reports "ready" once the command is running and "cancelled" after the
// first signal has cancelled ctx, then blocks.
func init() {
	subcommands["internal signal-block"] = func(a *app, ctx context.Context, _ []string) int {
		fmt.Fprintln(a.stderr, "ready")
		<-ctx.Done()
		// newSignalContext restores the default handler from another goroutine
		// right after cancelling ctx; wait for that so the second signal
		// terminates the process instead of being swallowed by the notifier.
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintln(a.stderr, "cancelled")
		<-time.After(time.Hour)
		return exitOK
	}
}
