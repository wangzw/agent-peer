package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// newBrokerCmdApp builds an app whose broker subcommand talks to stub.
func newBrokerCmdApp(stub *stubDeps) (*app, *bytes.Buffer, *bytes.Buffer) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	return &app{
		stdout:  stdout,
		stderr:  stderr,
		version: "v-test",
		deps:    stub.deps(),
	}, stdout, stderr
}

// passReadyFD duplicates w's descriptor for brokerCmd, which takes ownership of
// the descriptor and closes it. The test's own w is closed here instead of
// being left to its finalizer: the finalizer would close the descriptor number
// again later, after another goroutine may have reused it.
func passReadyFD(t *testing.T, w *os.File) uintptr {
	t.Helper()

	fd, err := syscall.Dup(int(w.Fd()))
	if err != nil {
		t.Fatalf("dup readiness pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close readiness pipe write end: %v", err)
	}
	return uintptr(fd)
}

// brokerLastLine returns the last non-empty line of s.
func brokerLastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// readBrokerPipeLine reads one line from r, failing if it takes longer than
// the test wait limit.
func readBrokerPipeLine(t *testing.T, r *os.File) string {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		t.Fatalf("read readiness pipe: %v", err)
	}
	return strings.TrimSuffix(line, "\n")
}

// waitBrokerPipeEOF fails unless r reaches EOF within the test wait limit.
func waitBrokerPipeEOF(t *testing.T, r *os.File) {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if n != 0 {
		t.Fatalf("read %q from readiness pipe, want EOF", buf[:n])
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read readiness pipe: got %v, want EOF", err)
	}
}

// TestBrokerPassesListenOverride covers REQ-broker-config-025: --listen
// replaces the configured address for this run.
func TestBrokerPassesListenOverride(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	code := a.brokerCmd(context.Background(), []string{"--listen", "127.0.0.1:17444"})
	if code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	got := stub.brokerCalls[0]
	if got.Listen != "127.0.0.1:17444" {
		t.Errorf("Listen = %q, want %q", got.Listen, "127.0.0.1:17444")
	}
	if got.Ready != nil {
		t.Errorf("Ready = %v, want nil", got.Ready)
	}
	if got.Version != a.version {
		t.Errorf("Version = %q, want %q", got.Version, a.version)
	}
}

func TestBrokerWithoutListenLeavesConfigInCharge(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), nil); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if got := stub.brokerCalls[0].Listen; got != "" {
		t.Errorf("Listen = %q, want empty", got)
	}
}

func TestBrokerRejectsEmptyListen(t *testing.T) {
	for _, args := range [][]string{{"--listen", ""}, {"--listen="}} {
		stub := &stubDeps{}
		a, _, stderr := newBrokerCmdApp(stub)

		if code := a.brokerCmd(context.Background(), args); code != exitUsage {
			t.Fatalf("brokerCmd(%q) returned %d, want %d", args, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "--listen requires a non-empty address") {
			t.Errorf("stderr = %q, want the empty --listen explanation", stderr.String())
		}
		if len(stub.brokerCalls) != 0 {
			t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
		}
	}
}

// TestBrokerStartupFailureExitCodes covers REQ-broker-config-013 and
// REQ-broker-config-024: each startup failure keeps its own exit code and the
// original reason text reaches standard error.
func TestBrokerStartupFailureExitCodes(t *testing.T) {
	cases := []struct {
		failure startFailure
		code    int
	}{
		{failAlreadyRunning, exitAlreadyRunning},
		{failListenUnavailable, exitListenUnavailable},
		{failInvalidConfig, exitInvalidConfig},
		{failInSession, exitInSession},
		{failNotRoot, exitNotRoot},
		{failRuntimeDir, exitRuntimeDir},
		{failInvalidListen, exitUsage},
		{failSystemUnavailable, exitFailure},
		{failOther, exitFailure},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("failure_%d", tc.failure), func(t *testing.T) {
			stub := &stubDeps{
				brokerErr:      errors.New("startup failed"),
				classifyResult: tc.failure,
			}
			a, _, stderr := newBrokerCmdApp(stub)

			if code := a.brokerCmd(context.Background(), nil); code != tc.code {
				t.Fatalf("brokerCmd returned %d, want %d", code, tc.code)
			}
			if want := "agent-peer broker: startup failed"; brokerLastLine(stderr.String()) != want {
				t.Errorf("last stderr line = %q, want %q", brokerLastLine(stderr.String()), want)
			}
		})
	}
}

func TestBrokerPassesSystemFlag(t *testing.T) {
	stub := &stubDeps{
		brokerErr:      errors.New("system mode unavailable"),
		classifyResult: failSystemUnavailable,
	}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"--system"}); code != exitFailure {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitFailure)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if !stub.brokerCalls[0].System {
		t.Error("System = false, want true")
	}
}

func TestBrokerOwnFailureReachesReadyPipe(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	code := a.brokerCmd(context.Background(), []string{"--listen=", fmt.Sprintf("--ready-fd=%d", passReadyFD(t, w))})
	if code != exitUsage {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitUsage)
	}
	line := readBrokerPipeLine(t, r)
	if !strings.HasPrefix(line, "--listen requires a non-empty address") {
		t.Errorf("readiness line = %q, want the --listen explanation", line)
	}
	waitBrokerPipeEOF(t, r)
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}

func TestBrokerNoReadyFdPassesNil(t *testing.T) {
	stub := &stubDeps{}
	a, _, _ := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), nil); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
	if stub.brokerCalls[0].Ready != nil {
		t.Errorf("Ready = %v, want nil", stub.brokerCalls[0].Ready)
	}
}

func TestBrokerClosesReadyPipeAfterRun(t *testing.T) {
	stub := &stubDeps{brokerErr: errors.New("boom")}
	a, _, _ := newBrokerCmdApp(stub)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	if code := a.brokerCmd(context.Background(), []string{fmt.Sprintf("--ready-fd=%d", passReadyFD(t, w))}); code != exitFailure {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitFailure)
	}
	waitBrokerPipeEOF(t, r)
	if len(stub.brokerCalls) != 1 {
		t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
	}
}

func TestBrokerRejectsClosedReadyFd(t *testing.T) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("get RLIMIT_NOFILE: %v", err)
	}
	// A descriptor number above the limit cannot have been opened, so no other
	// test running in parallel can reuse the closed pipe's number between the
	// close and this call.
	fd := limit.Cur + 1

	stub := &stubDeps{}
	a, _, stderr := newBrokerCmdApp(stub)
	if code := a.brokerCmd(context.Background(), []string{fmt.Sprintf("--ready-fd=%d", fd)}); code != exitUsage {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "is not an open file descriptor") {
		t.Errorf("stderr = %q, want the descriptor explanation", stderr.String())
	}
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}

func TestBrokerRejectsStandardStreamReadyFd(t *testing.T) {
	// A readiness pipe is inherited at fd 3 or higher; 0, 1 and 2 are the
	// standard streams. Before the check existed, brokerCmd accepted them and
	// closed them as the readiness pipe. Duplicate the test process's own
	// streams so a run against the unfixed code does not shut down its output.
	saved := make([]int, 3)
	for fd := range saved {
		dup, err := syscall.Dup(fd)
		if err != nil {
			t.Fatalf("dup fd %d: %v", fd, err)
		}
		saved[fd] = dup
	}
	t.Cleanup(func() {
		for fd, dup := range saved {
			if err := unix.Dup2(dup, fd); err != nil {
				t.Errorf("restore fd %d: %v", fd, err)
			}
			_ = syscall.Close(dup)
		}
	})

	for _, fd := range []uintptr{0, 1, 2} {
		stub := &stubDeps{}
		a, _, stderr := newBrokerCmdApp(stub)

		if code := a.brokerCmd(context.Background(), []string{fmt.Sprintf("--ready-fd=%d", fd)}); code != exitUsage {
			t.Fatalf("--ready-fd=%d returned %d, want %d", fd, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "standard stream") {
			t.Errorf("--ready-fd=%d stderr = %q, want the standard stream explanation", fd, stderr.String())
		}
		if len(stub.brokerCalls) != 0 {
			t.Errorf("--ready-fd=%d: runBroker called %d times, want 0", fd, len(stub.brokerCalls))
		}
	}
}

func TestBrokerRejectsPositionalArgument(t *testing.T) {
	stub := &stubDeps{}
	a, _, stderr := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"extra"}); code != exitUsage {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unexpected argument "extra"`) {
		t.Errorf("stderr = %q, want the unexpected argument message", stderr.String())
	}
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}

func TestBrokerHelpGoesToStdout(t *testing.T) {
	stub := &stubDeps{}
	a, stdout, stderr := newBrokerCmdApp(stub)

	if code := a.brokerCmd(context.Background(), []string{"-h"}); code != exitOK {
		t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "usage: agent-peer broker") {
		t.Errorf("stdout = %q, want the broker usage", stdout.String())
	}
	if strings.Contains(stdout.String(), "--ready-fd") {
		t.Errorf("stdout = %q, must not list the hidden flag", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if len(stub.brokerCalls) != 0 {
		t.Errorf("runBroker called %d times, want 0", len(stub.brokerCalls))
	}
}

func TestBrokerReadyFdEqualsForm(t *testing.T) {
	forms := []struct {
		name string
		args func(fd uintptr) []string
	}{
		{"equals", func(fd uintptr) []string { return []string{fmt.Sprintf("--ready-fd=%d", fd)} }},
		{"separate", func(fd uintptr) []string { return []string{"--ready-fd", fmt.Sprintf("%d", fd)} }},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			stub := &stubDeps{}
			a, _, _ := newBrokerCmdApp(stub)

			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe: %v", err)
			}
			defer func() { _ = r.Close() }()

			if code := a.brokerCmd(context.Background(), form.args(passReadyFD(t, w))); code != exitOK {
				t.Fatalf("brokerCmd returned %d, want %d", code, exitOK)
			}
			if len(stub.brokerCalls) != 1 {
				t.Fatalf("runBroker called %d times, want 1", len(stub.brokerCalls))
			}
			if stub.brokerCalls[0].Ready == nil {
				t.Error("Ready = nil, want the readiness pipe")
			}
		})
	}
}
