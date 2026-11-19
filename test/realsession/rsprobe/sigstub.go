package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() {
	register("sigstub", cmdSigstub)
}

// sigstubEvent is one JSON line in the sigstub log. Fields that do not belong
// to an event are omitted, except t_mono_ns which is present on every event.
type sigstubEvent struct {
	Event         string  `json:"event"`
	PID           int     `json:"pid,omitempty"`
	PPID          int     `json:"ppid,omitempty"`
	MessagingSock *string `json:"messaging_socket,omitempty"`
	Sig           string  `json:"sig,omitempty"`
	TMonNs        int64   `json:"t_mono_ns"`
}

// eventLog appends complete JSON lines to the log file without buffering, so a
// parent that reads the file while the stub is still running sees every event
// immediately. The mutex keeps the start event and the signal events, which are
// written from different goroutines, from interleaving.
type eventLog struct {
	mu sync.Mutex
	f  *os.File
	t0 time.Time
}

func (l *eventLog) write(ev sigstubEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.f.Write(b)
	return err
}

func (l *eventLog) sinceStart() int64 {
	return time.Since(l.t0).Nanoseconds()
}

// cmdSigstub implements "rsprobe sigstub --log L". It is a stdio MCP server
// that records the signals it receives instead of exiting on them.
func cmdSigstub(args []string) error {
	fs := flag.NewFlagSet("sigstub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	logPath := fs.String("log", "", "path to the JSON-lines event log")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *logPath == "" {
		return errors.New("sigstub requires --log")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return serveSigstub(*logPath)
}

// serveSigstub runs the MCP server and logs every catchable termination signal.
// It never returns normally; the probe ends it with SIGKILL.
func serveSigstub(logPath string) error {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	log := &eventLog{f: f, t0: time.Now()}

	// Install the handlers before announcing the start so a probe that signals
	// as soon as the start line appears cannot hit the default action. Signals
	// that arrive before the logging goroutine starts queue in the channel.
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	sock := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if err := log.write(sigstubEvent{
		Event:         "start",
		PID:           os.Getpid(),
		PPID:          os.Getppid(),
		MessagingSock: &sock,
	}); err != nil {
		return err
	}

	go func() {
		for sig := range sigCh {
			_ = log.write(sigstubEvent{
				Event:  "signal",
				Sig:    signalName(sig),
				TMonNs: log.sinceStart(),
			})
		}
	}()

	server := mcp.NewServer(&mcp.Implementation{Name: "sigstub", Version: "0"}, nil)
	mcp.AddTool[any, any](
		server,
		&mcp.Tool{Name: "ping", Description: "Reply with pong."},
		func(context.Context, *mcp.CallToolRequest, any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, nil, nil
		},
	)

	// Run returns when standard input closes or the transport fails. The stub
	// stays alive afterwards: the probe wants to see whether Claude Code sends a
	// signal once the MCP session is gone.
	_ = server.Run(context.Background(), &mcp.StdioTransport{})
	if err := log.write(sigstubEvent{
		Event:  "stdin-eof",
		TMonNs: log.sinceStart(),
	}); err != nil {
		return err
	}

	// The only intended exit is a signal that cannot be caught, such as SIGKILL.
	select {}
}

// signalName renders a signal the way the probe records it: SIGINT rather than
// the os.Signal default spelling ("interrupt").
func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	default:
		return sig.String()
	}
}
