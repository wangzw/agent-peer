package main

import (
	"context"
	"errors"

	"github.com/wangzw/peers/internal/mcpserver"
	"github.com/wangzw/peers/internal/mcpserver/termcmd"
)

// errUserLevelUpdateUnsupported reports that the selected Broker is this
// account's user-level Broker and declined to apply the update itself. The
// command then replaces the user-level binary in place, which is the route
// that existed before Brokers could apply updates.
var errUserLevelUpdateUnsupported = errors.New("the user-level Broker does not apply updates itself")

// errNoRunningBroker reports that no Broker could be selected or reached, so
// there is nothing to ask for an update. The command replaces the binary
// locally instead.
var errNoRunningBroker = errors.New("no running Broker")

// updateUserSocketPath is the socket path of this account's user-level Broker.
// It is a variable so a test can decide which selected Broker counts as
// user-level without depending on the account running the test.
var updateUserSocketPath = mcpserver.UserSocketPath

// applyUpdateThroughBroker asks the selected Broker to install the newest
// release itself. The request carries no version: the Broker decides what is
// newer and refuses a downgrade. When the selected Broker is this account's
// user-level Broker and refuses with failed_precondition, the command falls
// back to a local replacement.
func applyUpdateThroughBroker(ctx context.Context, euid func() int) (brokerUpdateResult, error) {
	var selectedPath string
	selectBroker := termcmd.Selector(func(ctx context.Context) (string, termcmd.Dialer, error) {
		path, dial, err := findBroker(ctx)
		if err == nil {
			selectedPath = path
		}
		return path, dial, err
	})

	result, err := termcmd.RunApplyUpdate(ctx, selectBroker)
	if err != nil {
		var answered *termcmd.ApplyUpdateError
		switch {
		case errors.As(err, &answered):
			if euid() != 0 && selectedPath != "" && selectedPath == updateUserSocketPath() && termcmd.IsFailedPrecondition(err) {
				return brokerUpdateResult{}, errUserLevelUpdateUnsupported
			}
			return brokerUpdateResult{}, err
		default:
			// The failure is in selection, the connection or the version
			// handshake, so no Broker answered the call.
			return brokerUpdateResult{}, errNoRunningBroker
		}
	}
	return brokerUpdateResult{
		FromVersion: result.FromVersion,
		ToVersion:   result.ToVersion,
		Restarting:  result.Restarting,
		UserLevel:   selectedPath != "" && selectedPath == updateUserSocketPath(),
	}, nil
}

// fetchBrokerUpdateStatus reads the version the selected Broker runs now.
func fetchBrokerUpdateStatus(ctx context.Context) (brokerUpdateStatus, error) {
	current, _, err := termcmd.RunUpdateStatus(ctx, findBroker)
	if err != nil {
		return brokerUpdateStatus{}, err
	}
	return brokerUpdateStatus{CurrentVersion: current}, nil
}
