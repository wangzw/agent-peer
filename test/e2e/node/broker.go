package node

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	// brokerStderrPrefix starts the line a Broker writes to standard error when
	// it fails to start.
	brokerStderrPrefix = "agent-peer broker: "
	// brokerProbeInterval is how often StartBroker tries the local socket while
	// the Broker process is starting.
	brokerProbeInterval = 50 * time.Millisecond
	// brokerProbeTimeout bounds one readiness probe. A Broker that has not
	// created its socket yet fails the probe quickly.
	brokerProbeTimeout = 250 * time.Millisecond
	// defaultBrokerBound is the readiness bound used when the caller passes 0:
	// the ten-second readiness pipe allowance plus three seconds.
	defaultBrokerBound = 13 * time.Second
	// brokerKillWait is how long StartBroker waits for a killed Broker to be
	// reaped before giving up.
	brokerKillWait = 2 * time.Second
)

// BrokerSpec describes one Broker process to start.
type BrokerSpec struct {
	// Path is the agent-peer binary.
	Path string
	// Args follow "broker" on the command line, for example
	// ["--listen", "127.0.0.1:41234"].
	Args []string
	// Env is the complete environment, normally built by BaseEnv.Build.
	Env []string
	// LogPath receives standard output and standard error, appended.
	LogPath string
	// Socket is the local socket used to decide that the Broker is ready.
	Socket string
}

// StartResult reports how StartBroker ended. When the Broker process exited
// before answering on its local socket, Running is false and ExitCode and
// StderrLast describe the failure.
type StartResult struct {
	Running    bool
	PID        int
	ExitCode   int
	StderrLast string
}

// BrokerProc is one started Broker process.
type BrokerProc struct {
	cmd     *exec.Cmd
	logPath string
	pid     int

	done    chan struct{}
	waitErr error
}

// StartBroker starts the Broker and waits, until bound expires, for either the
// process to exit or its local socket to answer Hello. The process runs in its
// own session, so signals sent to the node agent do not reach it.
//
// A Broker that exits before becoming ready is not an error: the returned
// StartResult carries its exit code and the last failure line. An error is
// returned only when the process could not be started or bound expired.
func StartBroker(ctx context.Context, spec BrokerSpec, bound time.Duration) (*BrokerProc, StartResult, error) {
	if bound <= 0 {
		bound = defaultBrokerBound
	}

	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, StartResult{}, fmt.Errorf("node: opening broker log %s: %w", spec.LogPath, err)
	}

	cmd := exec.Command(spec.Path, append([]string{"broker"}, spec.Args...)...)
	cmd.Env = spec.Env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	setDeathSignal(cmd.SysProcAttr)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, StartResult{}, fmt.Errorf("node: starting broker %s: %w", spec.Path, err)
	}
	// The child holds its own descriptor; the parent copy is not needed.
	_ = logFile.Close()

	proc := &BrokerProc{
		cmd:     cmd,
		logPath: spec.LogPath,
		pid:     cmd.Process.Pid,
		done:    make(chan struct{}),
	}
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.done)
	}()

	deadline := time.Now().Add(bound)
	ticker := time.NewTicker(brokerProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-proc.done:
			return proc, StartResult{
				Running:    false,
				PID:        proc.pid,
				ExitCode:   proc.exitCode(),
				StderrLast: lastBrokerStderr(proc.logPath),
			}, nil
		case <-ticker.C:
			if brokerHello(ctx, spec.Socket) {
				return proc, StartResult{Running: true, PID: proc.pid}, nil
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return nil, StartResult{}, proc.abandon(bound, spec.Socket)
			}
		case <-ctx.Done():
			return nil, StartResult{}, proc.abandon(bound, spec.Socket)
		}
	}
}

// abandon kills a Broker that never became ready and returns the error
// StartBroker reports for it.
func (b *BrokerProc) abandon(bound time.Duration, socket string) error {
	_ = b.cmd.Process.Kill()
	select {
	case <-b.done:
	case <-time.After(brokerKillWait):
	}
	if socket == "" {
		return fmt.Errorf("node: broker process %d did not become ready within %s", b.pid, bound)
	}
	return fmt.Errorf("node: broker process %d did not answer on %s within %s", b.pid, socket, bound)
}

// brokerHello reports whether the local socket accepts a Hello.
func brokerHello(ctx context.Context, socket string) bool {
	if socket == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, brokerProbeTimeout)
	defer cancel()
	oracle, err := DialOracle(socket)
	if err != nil {
		return false
	}
	defer func() { _ = oracle.Close() }()
	_, err = oracle.Hello(probeCtx)
	return err == nil
}

// lastBrokerStderr returns the last line of the log that starts with
// brokerStderrPrefix, without the prefix. It returns "" when the log has no
// such line or cannot be read.
func lastBrokerStderr(logPath string) string {
	file, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	last := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if rest, ok := strings.CutPrefix(line, brokerStderrPrefix); ok {
			last = rest
		}
	}
	return last
}

// PID returns the process ID of the Broker.
func (b *BrokerProc) PID() int { return b.pid }

// Signal sends sig to the Broker process.
func (b *BrokerProc) Signal(sig syscall.Signal) error {
	if b.cmd.Process == nil {
		return errors.New("node: broker process has no process handle")
	}
	return b.cmd.Process.Signal(sig)
}

// Wait waits up to bound for the Broker to exit and returns its exit code. A
// process terminated by a signal reports -1.
func (b *BrokerProc) Wait(bound time.Duration) (int, error) {
	select {
	case <-b.done:
		return b.exitCode(), nil
	case <-time.After(bound):
		return -1, fmt.Errorf("node: broker process %d did not exit within %s", b.pid, bound)
	}
}

// exitCode returns the exit code of the finished process, or -1 when a signal
// terminated it. It must only be called after done is closed.
func (b *BrokerProc) exitCode() int {
	var exitErr *exec.ExitError
	switch {
	case b.waitErr == nil:
		return 0
	case errors.As(b.waitErr, &exitErr):
		return exitErr.ExitCode()
	default:
		return -1
	}
}

// Proc is one process as read from the operating system. Env is nil when the
// environment could not be read, which happens for processes of other users.
type Proc struct {
	PID  int
	PPID int
	SID  int
	UID  int
	Args []string
	Env  map[string]string
}
