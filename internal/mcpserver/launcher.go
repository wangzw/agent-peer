package mcpserver

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// LauncherArgs are the arguments of the hidden subcommand that runs the
// intermediate process of the two-level re-exec: "agent-peer internal broker-launcher".
// cmd dispatches them to RunLauncher.
var LauncherArgs = []string{"internal", "broker-launcher"}

// ReadyFDFlag and ReadyFD: the launched Broker runs as "<exe> broker --ready-fd=3".
// cmd parses the flag and hands os.NewFile(3, "ready") to broker.RunOptions.Ready;
// the Broker writes one line, "ok" or a failure reason, and closes it.
const ReadyFDFlag = "--ready-fd"

// ReadyFD is the descriptor that carries the readiness pipe into the
// intermediate process and from there into the Broker.
const ReadyFD = 3

// RunLauncher is the body of the intermediate process. It expects the readiness
// pipe on fd ReadyFD, starts "<own executable> broker --ready-fd=3" in a new
// session and returns the process exit code without waiting for the Broker.
func RunLauncher() int {
	exe, err := os.Executable()
	if err != nil {
		reportLaunchFailure("cannot start the Broker: " + err.Error())
		return 1
	}
	return runLauncherWith(exe)
}

// runLauncherWith starts the Broker executable at exe. Tests use it to point
// the launcher at a path that does not exist.
func runLauncherWith(exe string) int {
	ready := os.NewFile(ReadyFD, "ready")
	if ready == nil {
		return 1
	}

	cmd := exec.Command(exe, "broker", ReadyFDFlag+"=3")
	cmd.ExtraFiles = []*os.File{ready}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "/"
	cmd.Env = brokerEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		_, _ = io.WriteString(ready, "cannot start the Broker: "+err.Error()+"\n")
		ready.Close()
		return 1
	}
	ready.Close()
	return 0
}

// reportLaunchFailure writes the reason to the readiness pipe, if there is one,
// so the MCP server process can show it to the model.
func reportLaunchFailure(reason string) {
	ready := os.NewFile(ReadyFD, "ready")
	if ready == nil {
		return
	}
	_, _ = io.WriteString(ready, reason+"\n")
	ready.Close()
}
