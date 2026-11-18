// Command agent-peer is the single entry point for the Broker process, the MCP
// server and the terminal commands.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=<version>".
var version = "dev"

const usage = `usage: agent-peer <command> [arguments]

Commands:
  --version  print the version and exit
  broker     run the Broker
  mcp        run the MCP server for one Claude Code session
  status     show the state of a running Broker
  audit      read the audit log
  service    install or manage the system-level Broker service
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "--version":
		fmt.Printf("agent-peer %s\n", version)
	case "broker", "mcp", "status", "audit", "service":
		fmt.Fprintf(os.Stderr, "agent-peer: %s is not implemented\n", os.Args[1])
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "agent-peer: unknown command %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
