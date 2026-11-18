package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func init() {
	testRoles["launcher"] = launchRole
	testRoles["launcher-env"] = launchEnvRole
	testRoles["launcher-hang"] = launchHangRole
	testRoles["broker"] = launchBrokerRole
}

// launchHangRole is the intermediate process that never exits: it reports
// readiness on fd 3 and then blocks. Launch must bound the wait for cmd.Wait
// instead of trusting the intermediate process to exit.
func launchHangRole() int {
	if ready := os.NewFile(ReadyFD, "ready"); ready != nil {
		_, _ = io.WriteString(ready, "ok\n")
		ready.Close()
	}
	if path := os.Getenv("FAKE_LAUNCHER_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	time.Sleep(time.Minute)
	return 0
}

// launchEnvRole is the intermediate process of TestLauncherDropsSessionVariables.
// The test gives its own environment the session variables, so unlike launchRole
// it must start the Broker directly: it switches the role first, then calls
// runLauncherWith on the test binary itself.
func launchEnvRole() int {
	os.Setenv("AGENT_PEER_TEST_ROLE", "broker")
	return runLauncherWith(os.Args[0])
}

// launchRole runs the intermediate process. In production it is RunLauncher;
// tests point it at a nonexistent executable through FAKE_LAUNCHER_EXE.
func launchRole() int {
	if exe := os.Getenv("FAKE_LAUNCHER_EXE"); exe != "" {
		return runLauncherWith(exe)
	}
	// The Broker started by RunLauncher inherits this environment, so the
	// second-level process must see the broker role instead of launcher.
	os.Setenv("AGENT_PEER_TEST_ROLE", "broker")
	return RunLauncher()
}

type fakeBrokerFacts struct {
	PID                   int      `json:"pid"`
	PPID                  int      `json:"ppid"`
	SID                   int      `json:"sid"`
	Args                  []string `json:"args"`
	HasMessagingSocketEnv bool     `json:"has_messaging_socket_env"`
	HasChildSessionEnv    bool     `json:"has_child_session_env"`
	HasAgentPeerSocketEnv bool     `json:"has_agent_peer_socket_env"`
	FD3IsPipe             bool     `json:"fd3_is_pipe"`
	CWD                   string   `json:"cwd"`
}

// launchBrokerRole is the test double for the launched Broker. It records the
// facts the tests assert on, writes its readiness line, then stays alive until
// the test kills it or FAKE_BROKER_LIFETIME elapses.
func launchBrokerRole() int {
	ready := os.NewFile(ReadyFD, "ready")
	facts := fakeBrokerFacts{
		PID:  os.Getpid(),
		PPID: os.Getppid(),
		Args: os.Args,
		FD3IsPipe: func() bool {
			info, err := ready.Stat()
			return err == nil && info.Mode()&os.ModeNamedPipe != 0
		}(),
	}
	if sid, err := unix.Getsid(0); err == nil {
		facts.SID = sid
	}
	if cwd, err := os.Getwd(); err == nil {
		facts.CWD = cwd
	}
	_, facts.HasMessagingSocketEnv = os.LookupEnv("CLAUDE_CODE_MESSAGING_SOCKET")
	_, facts.HasChildSessionEnv = os.LookupEnv("CLAUDE_CODE_CHILD_SESSION")
	_, facts.HasAgentPeerSocketEnv = os.LookupEnv("AGENT_PEER_SOCKET")

	if path := os.Getenv("FAKE_BROKER_FACTS"); path != "" {
		if data, err := json.Marshal(facts); err == nil {
			_ = os.WriteFile(path, data, 0o600)
		}
	}

	switch readyText := os.Getenv("FAKE_BROKER_READY"); readyText {
	case "exit":
		os.Exit(1)
	case "none":
		// Keep the pipe open and write nothing so the reader waits.
	case "ok":
		_, _ = io.WriteString(ready, "ok\n")
		ready.Close()
	default:
		_, _ = io.WriteString(ready, readyText+"\n")
		ready.Close()
	}

	lifetime := 5 * time.Second
	if text := os.Getenv("FAKE_BROKER_LIFETIME"); text != "" {
		if d, err := time.ParseDuration(text); err == nil {
			lifetime = d
		}
	}
	time.Sleep(lifetime)
	return 0
}

func launchTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ap-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func launchTestEnv(t *testing.T, factsPath, ready string, extra ...string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"AGENT_PEER_TEST_ROLE=launcher",
		"FAKE_BROKER_READY=" + ready,
		"FAKE_BROKER_LIFETIME=60s",
	}
	if factsPath != "" {
		env = append(env, "FAKE_BROKER_FACTS="+factsPath)
	}
	return append(env, extra...)
}

func launchReadFacts(t *testing.T, path string) fakeBrokerFacts {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read facts: %v", err)
	}
	var facts fakeBrokerFacts
	if err := json.Unmarshal(data, &facts); err != nil {
		t.Fatalf("decode facts: %v", err)
	}
	return facts
}

func launchPPID(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps -o ppid= -p %d: %v", pid, err)
	}
	pp, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse ppid of %d: %v", pid, err)
	}
	return pp
}

func TestBrokerEnvStripsSessionVariables(t *testing.T) {
	t.Parallel()
	in := []string{
		"CLAUDE_CODE_MESSAGING_SOCKET=/x",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"AGENT_PEER_SOCKET=/y",
		"AGENT_PEER_TEST_RUNTIME_ROOT=/r",
		"PATH=/bin",
	}
	want := []string{"AGENT_PEER_TEST_RUNTIME_ROOT=/r", "PATH=/bin"}
	got := brokerEnv(in)
	if len(got) != len(want) {
		t.Fatalf("brokerEnv returned %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("brokerEnv returned %q, want %q", got, want)
		}
	}
}

// launchWaitFacts waits up to two seconds for the Broker to write its facts
// file. The launcher helper exits as soon as the Broker is started, so the file
// may appear a moment after the helper has been reaped.
func launchWaitFacts(t *testing.T, path string) fakeBrokerFacts {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var facts fakeBrokerFacts
			if err := json.Unmarshal(data, &facts); err != nil {
				t.Fatalf("decode facts: %v", err)
			}
			return facts
		}
		if time.Now().After(deadline) {
			t.Fatalf("Broker did not write facts to %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLauncherDropsSessionVariables is the end of the environment chain: the
// launcher helper itself has the session variables, and the Broker it starts
// through runLauncherWith must not see them. TestBrokerEnvStripsSessionVariables
// covers brokerEnv alone, which would pass even if runLauncherWith called it on
// something other than its own environment.
func TestLauncherDropsSessionVariables(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	factsPath := filepath.Join(dir, "facts.json")

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer readyR.Close()

	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"AGENT_PEER_TEST_ROLE=launcher-env",
		"FAKE_BROKER_READY=ok",
		"FAKE_BROKER_LIFETIME=60s",
		"FAKE_BROKER_FACTS=" + factsPath,
		"AGENT_PEER_SOCKET=/session/broker.sock",
		"CLAUDE_CODE_MESSAGING_SOCKET=/session/messaging.sock",
		"CLAUDE_CODE_CHILD_SESSION=1",
	}
	cmd.ExtraFiles = []*os.File{readyW}
	if err := cmd.Start(); err != nil {
		readyW.Close()
		t.Fatalf("start launcher helper: %v", err)
	}
	readyW.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("launcher helper: %v", err)
	}

	facts := launchWaitFacts(t, factsPath)
	t.Cleanup(func() { _ = syscall.Kill(facts.PID, syscall.SIGKILL) })

	if facts.HasAgentPeerSocketEnv {
		t.Error("Broker inherited AGENT_PEER_SOCKET")
	}
	if facts.HasMessagingSocketEnv {
		t.Error("Broker inherited CLAUDE_CODE_MESSAGING_SOCKET")
	}
	if facts.HasChildSessionEnv {
		t.Error("Broker inherited CLAUDE_CODE_CHILD_SESSION")
	}
}

// REQ-broker-config-012
func TestLaunchDetachesBroker(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	factsPath := filepath.Join(dir, "facts.json")
	env := launchTestEnv(t, factsPath, "ok",
		"CLAUDE_CODE_MESSAGING_SOCKET=/x",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"AGENT_PEER_SOCKET=/y",
	)
	l := &execLauncher{
		Executable:   os.Args[0],
		Env:          brokerEnv(env),
		ReadyTimeout: 2 * time.Second,
	}
	if err := l.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	facts := launchReadFacts(t, factsPath)
	t.Cleanup(func() { _ = syscall.Kill(facts.PID, syscall.SIGKILL) })

	var hasBroker, hasReadyFlag bool
	for _, arg := range facts.Args {
		switch arg {
		case "broker":
			hasBroker = true
		case ReadyFDFlag + "=3":
			hasReadyFlag = true
		}
	}
	if !hasBroker || !hasReadyFlag {
		t.Errorf("Broker args = %q, want both broker and %s=3", facts.Args, ReadyFDFlag)
	}
	if facts.HasMessagingSocketEnv {
		t.Error("Broker inherited CLAUDE_CODE_MESSAGING_SOCKET")
	}
	if facts.HasChildSessionEnv {
		t.Error("Broker inherited CLAUDE_CODE_CHILD_SESSION")
	}
	if facts.HasAgentPeerSocketEnv {
		t.Error("Broker inherited AGENT_PEER_SOCKET")
	}
	if !facts.FD3IsPipe {
		t.Error("fd 3 of the Broker is not the readiness pipe")
	}
	if facts.SID != facts.PID {
		t.Errorf("Broker sid = %d, pid = %d; want a new session", facts.SID, facts.PID)
	}
	if facts.CWD != "/" {
		t.Errorf("Broker working directory = %q, want /", facts.CWD)
	}

	pp := launchPPID(t, facts.PID)
	if pp == os.Getpid() {
		t.Fatalf("Broker parent pid = %d, which is the test process", pp)
	}
	if pp2 := launchPPID(t, pp); pp2 == os.Getpid() {
		t.Fatalf("Broker grandparent pid = %d, which is the test process", pp2)
	}
}

// REQ-broker-config-024
func TestLaunchReportsReason(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	const reason = "listen address :17443 is unavailable: address already in use"
	l := &execLauncher{
		Executable:   os.Args[0],
		Env:          brokerEnv(launchTestEnv(t, filepath.Join(dir, "facts.json"), reason)),
		ReadyTimeout: 2 * time.Second,
	}
	err := l.Launch(context.Background())
	var le *launchError
	if !errors.As(err, &le) {
		t.Fatalf("Launch error = %v, want *launchError", err)
	}
	if le.Reason != reason {
		t.Fatalf("reason = %q, want %q", le.Reason, reason)
	}
}

func TestLaunchTimeout(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	factsPath := filepath.Join(dir, "facts.json")
	l := &execLauncher{
		Executable:   os.Args[0],
		Env:          brokerEnv(launchTestEnv(t, factsPath, "none")),
		ReadyTimeout: 200 * time.Millisecond,
	}
	start := time.Now()
	err := l.Launch(context.Background())
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Launch waited %v, want the 200ms timeout", elapsed)
	}
	var le *launchError
	if !errors.As(err, &le) {
		t.Fatalf("Launch error = %v, want *launchError", err)
	}
	if !strings.Contains(le.Reason, "did not report readiness") {
		t.Fatalf("reason = %q, want a timeout reason", le.Reason)
	}
	facts := launchReadFacts(t, factsPath)
	t.Cleanup(func() { _ = syscall.Kill(facts.PID, syscall.SIGKILL) })
}

func TestLaunchEOFWithoutLine(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	l := &execLauncher{
		Executable:   os.Args[0],
		Env:          brokerEnv(launchTestEnv(t, filepath.Join(dir, "facts.json"), "exit")),
		ReadyTimeout: 2 * time.Second,
	}
	start := time.Now()
	err := l.Launch(context.Background())
	var le *launchError
	if !errors.As(err, &le) {
		t.Fatalf("Launch error = %v, want *launchError", err)
	}
	if le.Reason != readyEOFText {
		t.Fatalf("reason = %q, want %q", le.Reason, readyEOFText)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Launch waited %v for EOF; the write end was not closed", elapsed)
	}
}

func TestLauncherReportsExecFailure(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	l := &execLauncher{
		Executable: os.Args[0],
		Env: brokerEnv(launchTestEnv(t, filepath.Join(dir, "facts.json"), "ok",
			"FAKE_LAUNCHER_EXE=/nonexistent/agent-peer")),
		ReadyTimeout: 2 * time.Second,
	}
	err := l.Launch(context.Background())
	var le *launchError
	if !errors.As(err, &le) {
		t.Fatalf("Launch error = %v, want *launchError", err)
	}
	if !strings.HasPrefix(le.Reason, "cannot start the Broker:") {
		t.Fatalf("reason = %q, want a \"cannot start the Broker:\" prefix", le.Reason)
	}
}

// TestLaunchKillsStuckLauncher covers an intermediate process that reports
// readiness but never exits. Launch must stop waiting after a short grace
// period, kill it, and report a failure instead of hanging forever.
func TestLaunchKillsStuckLauncher(t *testing.T) {
	t.Parallel()
	dir := launchTempDir(t)
	pidPath := filepath.Join(dir, "launcher.pid")
	t.Cleanup(func() {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	l := &execLauncher{
		Executable: os.Args[0],
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + t.TempDir(),
			"AGENT_PEER_TEST_ROLE=launcher-hang",
			"FAKE_LAUNCHER_PID=" + pidPath,
		},
		ReadyTimeout: 200 * time.Millisecond,
	}

	start := time.Now()
	errc := make(chan error, 1)
	go func() { errc <- l.Launch(context.Background()) }()

	var err error
	select {
	case err = <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("Launch did not return within the readiness timeout plus the 2s grace")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Launch took %v, want ReadyTimeout plus the 2s grace", elapsed)
	}
	var le *launchError
	if !errors.As(err, &le) {
		t.Fatalf("Launch error = %v, want *launchError", err)
	}
	if !strings.Contains(le.Reason, "did not exit") {
		t.Fatalf("reason = %q, want it to say the launcher did not exit", le.Reason)
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("launcher pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse launcher pid: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("stuck launcher pid %d is still around: kill(0) = %v", pid, err)
	}
}

func TestLauncherArgs(t *testing.T) {
	t.Parallel()
	if len(LauncherArgs) != 2 || LauncherArgs[0] != "internal" || LauncherArgs[1] != "broker-launcher" {
		t.Fatalf("LauncherArgs = %q, want [internal broker-launcher]", LauncherArgs)
	}
	if ReadyFDFlag != "--ready-fd" {
		t.Fatalf("ReadyFDFlag = %q, want --ready-fd", ReadyFDFlag)
	}
	if ReadyFD != 3 {
		t.Fatalf("ReadyFD = %d, want 3", ReadyFD)
	}
}
