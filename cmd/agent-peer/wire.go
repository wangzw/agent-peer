package main

import (
	"context"
	"errors"
	"io"

	"github.com/wangzw/agent-peer/internal/broker"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/internal/mcpserver/termcmd"
)

// protocolOverrides returns the local and link protocol versions sent in the
// version handshake. Zero means the version of the generated localpb and
// linkpb packages, which is the only value a release build uses. The version
// compatibility unit replaces this function in agentpeer_testenv builds.
var protocolOverrides = func() (local, link uint32) { return 0, 0 }

// brokerRun is the process entry point behind the broker subcommand. It is a
// variable so a test can record the RunOptions the command line builds without
// starting a real Broker.
var brokerRun = broker.Run

// mcpserver.FindBrokerWithDial has the signature the terminal commands select
// with; the returned dialer checks the peer of the Broker socket.
var _ termcmd.Selector = mcpserver.FindBrokerWithDial

// realDeps connects the command line to the real Broker and MCP server entry
// points. It is called once by main before any subcommand runs.
func realDeps(version string) deps {
	return deps{
		runBroker: func(ctx context.Context, o brokerOptions) error {
			local, link := protocolOverrides()
			opts := broker.RunOptions{
				System:               o.System,
				Listen:               o.Listen,
				LogOutput:            o.LogOutput,
				Version:              o.Version,
				LocalProtocolVersion: local,
				LinkProtocolVersion:  link,
			}
			return brokerRun(ctx, opts)
		},
		classifyBrokerErr: classifyBrokerErr,
		runMCP: func(ctx context.Context) error {
			local, _ := protocolOverrides()
			return mcpserver.Run(ctx, mcpserver.Options{
				Version:              version,
				LocalProtocolVersion: local,
			})
		},
		runStatus: func(ctx context.Context, stdout io.Writer) error {
			return termcmd.RunStatus(ctx, mcpserver.FindBrokerWithDial, stdout)
		},
		runAudit: func(ctx context.Context, stdout io.Writer, limit uint32) error {
			return termcmd.RunAudit(ctx, mcpserver.FindBrokerWithDial, limit, stdout)
		},
	}
}

// classifyBrokerErr maps a Broker startup error to the exit-code family it
// belongs to. Run wraps every sentinel with its concrete cause, so the check
// must use errors.Is rather than ==. ErrLockLost and errors the Broker does not
// classify share the generic failure code.
func classifyBrokerErr(err error) startFailure {
	switch {
	case errors.Is(err, broker.ErrAlreadyRunning):
		return failAlreadyRunning
	case errors.Is(err, broker.ErrListenUnavailable):
		return failListenUnavailable
	case errors.Is(err, broker.ErrInvalidConfig):
		return failInvalidConfig
	case errors.Is(err, broker.ErrInSession):
		return failInSession
	case errors.Is(err, broker.ErrNotRoot):
		return failNotRoot
	case errors.Is(err, broker.ErrRuntimeDir):
		return failRuntimeDir
	case errors.Is(err, broker.ErrInvalidListen):
		return failInvalidListen
	case errors.Is(err, broker.ErrSystemModeUnavailable):
		return failSystemUnavailable
	default:
		return failOther
	}
}
