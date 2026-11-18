package mcpserver

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// launcher starts the user-level Broker of this account and reports whether it
// became ready. A reported failure is returned as *launchError.
type launcher interface {
	Launch(ctx context.Context) error
}

// launchError carries the reason the Broker reported instead of readiness.
// Error returns Reason unchanged, because the text is shown to the model.
type launchError struct{ Reason string }

func (e *launchError) Error() string { return e.Reason }

const (
	readyTimeoutDefault = 10 * time.Second
	readyTimeoutText    = "the Broker did not report readiness within 10s"
	readyEOFText        = "the Broker exited before reporting whether it started"

	// launcherExitGrace bounds how long the intermediate process may take to
	// exit once the readiness line has been read. It normally exits at once;
	// one that does not is killed so Launch cannot wait forever.
	launcherExitGrace = 2 * time.Second
	launcherStuckText = "the Broker launcher did not exit after starting the Broker"
)

// execLauncher starts the intermediate process of the two-level re-exec and
// waits for the Broker's readiness line on the pipe passed through fd 3.
type execLauncher struct {
	Executable   string        // path of the agent-peer binary; production: os.Executable()
	LauncherArgs []string      // production: LauncherArgs ("internal", "broker-launcher")
	Env          []string      // production: brokerEnv(os.Environ())
	ReadyTimeout time.Duration // production: 10 * time.Second
	after        func(time.Duration) <-chan time.Time
}

// Launch re-executes the agent-peer binary with LauncherArgs, hands it the
// write end of a pipe as fd 3, and reads one line from the read end within
// ReadyTimeout. "ok" means the Broker is ready; any other line is the reason
// the Broker reported. EOF without a line and the timeout are failures too.
func (l *execLauncher) Launch(ctx context.Context) error {
	r, w, err := os.Pipe()
	if err != nil {
		return &launchError{Reason: "cannot create the readiness pipe: " + err.Error()}
	}
	defer func() { _ = r.Close() }()

	cmd := exec.Command(l.Executable, l.LauncherArgs...)
	cmd.ExtraFiles = []*os.File{w}
	cmd.Env = l.Env
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return &launchError{Reason: "cannot start the Broker launcher: " + err.Error()}
	}
	// Closing the parent's copy of the write end is what lets the reader see
	// EOF once every process that inherited the pipe has exited.
	_ = w.Close()

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	linec := make(chan launchReadResult, 1)
	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
				linec <- launchReadResult{line: trimmed}
				return
			}
			if err != nil {
				linec <- launchReadResult{eof: true}
				return
			}
		}
	}()

	timeout := l.ReadyTimeout
	if timeout <= 0 {
		timeout = readyTimeoutDefault
	}
	after := l.after
	if after == nil {
		after = time.After
	}

	var reason string
	select {
	case res := <-linec:
		switch {
		case res.eof:
			reason = readyEOFText
		case res.line == "ok":
			// Ready; reason stays empty.
		default:
			reason = res.line
		}
	case <-after(timeout):
		reason = readyTimeoutText
	case <-ctx.Done():
		return ctx.Err()
	}

	// The intermediate process exits right after it starts the Broker. Reap it
	// before returning so a detached Broker is not left parented to a zombie.
	// A stuck launcher is killed after a short grace period: it has already
	// done its job (or failed to), and waiting for it would hang the MCP server.
	exit := time.NewTimer(launcherExitGrace)
	defer exit.Stop()
	select {
	case <-waitDone:
	case <-ctx.Done():
		return ctx.Err()
	case <-exit.C:
		_ = cmd.Process.Kill()
		<-waitDone
		if reason == "" {
			reason = launcherStuckText
		}
	}

	if reason != "" {
		return &launchError{Reason: reason}
	}
	return nil
}

type launchReadResult struct {
	line string
	eof  bool
}

// brokerEnvDropped lists the variables a launched Broker must not inherit.
// CLAUDE_CODE_MESSAGING_SOCKET would make the Broker look like part of a
// session, CLAUDE_CODE_CHILD_SESSION marks the process as a subagent, and
// AGENT_PEER_SOCKET is meaningless to the Broker itself.
var brokerEnvDropped = map[string]bool{
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"AGENT_PEER_SOCKET":            true,
}

// brokerEnv returns env without the session variables above; every other entry
// is kept in its original order.
func brokerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if brokerEnvDropped[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
