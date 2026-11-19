package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

const mcpUsage = "usage: agent-peer mcp"

func init() {
	subcommands["mcp"] = (*app).mcpCmd
}

// mcpCmd runs the MCP server over the process's own stdin and stdout. It must
// never write anything else to stdout, because that stream carries the MCP
// protocol.
func (a *app) mcpCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(a.stdout, mcpUsage)
			return exitOK
		}
		_, _ = fmt.Fprintf(a.stderr, "%s\nagent-peer mcp: %s\n", mcpUsage, err)
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(a.stderr, "%s\nagent-peer mcp: unexpected argument %q\n", mcpUsage, fs.Arg(0))
		return exitUsage
	}
	if err := a.deps.runMCP(ctx); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer mcp: %s\n", err)
		return exitFailure
	}
	return exitOK
}
