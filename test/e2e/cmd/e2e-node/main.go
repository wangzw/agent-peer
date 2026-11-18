// Command e2e-node is the resident node agent the end-to-end tests drive from
// the host. It exposes the node library over the control protocol on a Unix
// socket inside the container.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/ctl"
	"github.com/wangzw/agent-peer/test/e2e/node"
)

// shutdownBound bounds the HTTP server shutdown.
const shutdownBound = 2 * time.Second

func main() {
	os.Exit(run(os.Args[1:]))
}

// run carries out one invocation of the command and returns its exit code.
func run(args []string) int {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: e2e-node serve --ctl <socket> --home <dir> --bin <agent-peer> [--runtime-root <dir>] [--path <PATH>]")
		return 2
	}

	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("ctl", "", "control socket path")
	home := flags.String("home", "", "account home directory")
	binary := flags.String("bin", "", "agent-peer binary")
	runtimeRoot := flags.String("runtime-root", "", "value of AGENT_PEER_TEST_RUNTIME_ROOT")
	path := flags.String("path", os.Getenv("PATH"), "value of PATH for child processes")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *socket == "" || *home == "" || *binary == "" {
		fmt.Fprintln(os.Stderr, "e2e-node: --ctl, --home and --bin are required")
		return 2
	}

	logDir := filepath.Dir(*socket)
	logFile, err := os.OpenFile(filepath.Join(logDir, "node.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e-node: opening the log: %v\n", err)
		return 1
	}
	defer func() { _ = logFile.Close() }()
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(logFile, format+"\n", args...)
	}

	// A socket left behind by a previous run would make Listen fail.
	if err := os.Remove(*socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "e2e-node: removing the stale socket: %v\n", err)
		return 1
	}
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e-node: listening on %s: %v\n", *socket, err)
		return 1
	}
	if err := os.Chmod(*socket, 0o600); err != nil {
		_ = listener.Close()
		fmt.Fprintf(os.Stderr, "e2e-node: setting the socket mode: %v\n", err)
		return 1
	}

	extra := make(map[string]string)
	if *runtimeRoot != "" {
		extra["AGENT_PEER_TEST_RUNTIME_ROOT"] = *runtimeRoot
	}
	env := node.BaseEnv{Home: *home, Path: *path, Extra: extra}
	tb := node.NewTB("e2e-node", "")
	agent := node.NewAgent(tb, env, *binary, logDir)

	server := &http.Server{
		Handler:           ctl.NewHandler(agent),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	logf("e2e-node: serving on %s", *socket)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
			agent.Shutdown()
		case <-agent.Done():
		}
	}()

	exit := 0
	select {
	case <-agent.Done():
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("e2e-node: serving: %v", err)
			exit = 1
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
	defer cancel()
	_ = server.Shutdown(ctx)
	if err := agent.Close(); err != nil {
		logf("e2e-node: closing: %v", err)
	}
	if err := os.Remove(*socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		logf("e2e-node: removing the socket: %v", err)
	}
	logf("e2e-node: stopped")
	return exit
}
