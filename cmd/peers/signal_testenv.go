//go:build peers_testenv

package main

import (
	"context"
	"fmt"
	"time"
)

// signalRestoreDone receives one value when signalRestoreHook fires, that is,
// after newSignalContext has restored the default signal handling. The channel
// is buffered so the hook never blocks, and it is sent to rather than closed so
// a test binary that creates more than one signal context cannot panic.
var signalRestoreDone = make(chan struct{}, 1)

// signal-block is a hidden subcommand that exists only in test builds. A
// subprocess test uses it to observe the process-level signal contract: the
// first SIGINT or SIGTERM cancels ctx, and the second terminates the process.
// It reports "ready" once the command is running and "cancelled" after the
// first signal has cancelled ctx, then blocks.
func init() {
	signalRestoreHook = func() {
		select {
		case signalRestoreDone <- struct{}{}:
		default:
		}
	}

	subcommands["internal signal-block"] = func(a *app, ctx context.Context, _ []string) int {
		_, _ = fmt.Fprintln(a.stderr, "ready")
		<-ctx.Done()
		// newSignalContext restores the default handler from another goroutine
		// right after cancelling ctx; wait for the hook so the second signal
		// cannot race the restore and be swallowed by the notifier.
		<-signalRestoreDone
		_, _ = fmt.Fprintln(a.stderr, "cancelled")
		<-time.After(time.Hour)
		return exitOK
	}
}
