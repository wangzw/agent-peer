// Command peers is the single entry point for the Broker process, the MCP
// server and the terminal commands.
package main

import (
	"context"
	"os"
)

// version is set at build time with -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	ctx, stop := newSignalContext(context.Background())
	a := &app{
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		version: version,
		deps:    realDeps(version),
	}
	code := a.run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
