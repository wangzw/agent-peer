package main

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/wangzw/peers/internal/broker"
	"github.com/wangzw/peers/internal/broker/update"
	"github.com/wangzw/peers/internal/mcpserver"
	"github.com/wangzw/peers/internal/mcpserver/termcmd"
)

// protocolOverrides returns the local and link protocol versions sent in the
// version handshake. Zero means the version of the generated localpb and
// linkpb packages, which is the only value a release build uses. The version
// compatibility unit replaces this function in peers_testenv builds.
var protocolOverrides = func() (local, link uint32) { return 0, 0 }

// brokerRun is the process entry point behind the broker subcommand. It is a
// variable so a test can record the RunOptions the command line builds without
// starting a real Broker.
var brokerRun = broker.Run

// mcpserver.FindBrokerWithDial has the signature the terminal commands select
// with; the returned dialer checks the peer of the Broker socket.
var _ termcmd.Selector = mcpserver.FindBrokerWithDial

// findBroker is the account-based selector the terminal commands use. It is a
// variable so a test can point the real dep closures at a fake Broker while
// exercising the production wiring.
var findBroker termcmd.Selector = mcpserver.FindBrokerWithDial

// findBrokerForLevel is the selector the commands that take --system or --user
// use. It is a variable for the same reason as findBroker.
var findBrokerForLevel = mcpserver.FindBrokerWithDialForLevel

var _ func(context.Context, bool) (string, termcmd.Dialer, error) = findBrokerForLevel

// selectorForLevel turns the level-aware selector into the termcmd.Selector the
// shared runners take.
func selectorForLevel(system bool) termcmd.Selector {
	return func(ctx context.Context) (string, termcmd.Dialer, error) {
		return findBrokerForLevel(ctx, system)
	}
}

// realDeps connects the command line to the real Broker and MCP server entry
// points. It is called once by main before any subcommand runs.
func realDeps(version string) deps {
	d := deps{
		euid: os.Geteuid,
		runBroker: func(ctx context.Context, o brokerOptions) error {
			local, link := protocolOverrides()
			opts := broker.RunOptions{
				System:               o.System,
				Listen:               o.Listen,
				AllowDegraded:        o.AllowDegraded,
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
		runStatus: func(ctx context.Context, system bool, stdout io.Writer) error {
			return termcmd.RunStatus(ctx, selectorForLevel(system), stdout)
		},
		runAudit: func(ctx context.Context, system bool, stdout io.Writer, limit uint32) error {
			return termcmd.RunAudit(ctx, selectorForLevel(system), limit, stdout)
		},
		runConfigGet: func(ctx context.Context, key string, stdout io.Writer) error {
			return termcmd.RunConfigGet(ctx, findBroker, key, stdout)
		},
		runConfigSet: func(ctx context.Context, key, value string, stdout io.Writer) error {
			return termcmd.RunConfigSet(ctx, findBroker, key, value, stdout)
		},
		runSetOnline: func(ctx context.Context, system, online bool, stdout io.Writer) error {
			return termcmd.RunSetOnline(ctx, selectorForLevel(system), online, stdout)
		},
		runPairingKeyCreate: func(ctx context.Context, stdout io.Writer) error {
			return termcmd.RunPairingKeyCreate(ctx, findBroker, stdout)
		},
		runPeerList: func(ctx context.Context, stdout io.Writer) error {
			return termcmd.RunPeerList(ctx, findBroker, stdout)
		},
		runPeerConnect: func(ctx context.Context, address, key, alias string, stdout, warn io.Writer) error {
			return termcmd.RunPeerConnect(ctx, findBroker, address, key, alias, stdout, warn)
		},
		runPeerDisconnect: func(ctx context.Context, broker string, stdout io.Writer) error {
			return termcmd.RunPeerDisconnect(ctx, findBroker, broker, stdout)
		},
		peerNames: func(ctx context.Context) ([]string, error) {
			return termcmd.PeerNames(ctx, findBroker)
		},

		updateAPIBase:      update.APIBase,
		updateLatest:       update.Latest,
		updateNewer:        update.Newer,
		updateApply:        update.Apply,
		brokerUpdateStatus: fetchBrokerUpdateStatus,
	}
	d.applyUpdateViaBroker = func(ctx context.Context) (brokerUpdateResult, error) {
		return applyUpdateThroughBroker(ctx, d.euid)
	}
	return d
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
	default:
		return failOther
	}
}
