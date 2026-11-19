//go:build e2e

package syscontainer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// evidenceCommandBound bounds one diagnostic command. A failing test must still
// end promptly even when the daemon is slow to answer.
const evidenceCommandBound = 10 * time.Second

// logBootEvidence writes the state of the systemd container to the test log. It
// runs while the container still exists, before the deferred teardown removes
// it.
func (c *container) logBootEvidence() {
	c.t.Helper()
	c.t.Logf("syscontainer: %s", c.bootEvidence())
}

// bootEvidence collects the state that explains why systemd did not become
// ready. It never fails the test: every command's error text becomes part of
// the evidence, so a container that is already gone still leaves a record.
func (c *container) bootEvidence() string {
	var e bootEvidence

	id := strings.TrimSpace(c.docker("compose", "-p", c.project, "-f", c.composeFile, "ps", "-aq", "systemd"))
	if id == "" {
		e.State = "docker compose ps -aq systemd returned no container"
		e.Logs = c.docker("compose", "-p", c.project, "-f", c.composeFile, "ps", "-a")
	} else {
		e.State = c.docker("inspect", "--format", "{{json .State}}", id)
		e.Logs = c.docker("logs", "--tail", strconv.Itoa(evidenceTailLines), id)
		// Without --wait is-system-running reports the current state instead
		// of blocking, so a container that is still booting shows it.
		e.SystemState = c.docker("exec", id, "systemctl", "is-system-running")
		e.Journal = c.docker("exec", id, "journalctl", "-b", "--no-pager", "-n", "200")
	}

	e.HostMemory = readHostMemory()
	e.HostPids = readHostPids()

	running := strings.Fields(c.docker("ps", "-q"))
	e.Containers = fmt.Sprintf("%d running (docker ps -q)", len(running))

	return formatBootEvidence(e)
}

// docker runs one docker command with the container's environment and returns
// its combined output. The error text is part of the result: a command that
// failed is evidence too.
func (c *container) docker(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), evidenceCommandBound)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err == nil {
		return text
	}
	if text == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v\n%s", err, text)
}

// readHostMemory returns the first /proc/meminfo lines: MemTotal, MemFree and
// MemAvailable.
func readHostMemory() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return err.Error()
	}
	return tailLines(strings.TrimSpace(string(data)), 3)
}

// readHostPids reports the host pids accounting that a container kill for a
// process limit would show against.
func readHostPids() string {
	var b strings.Builder
	for _, path := range []string{
		"/sys/fs/cgroup/pids.current",
		"/sys/fs/cgroup/pids.max",
		"/sys/fs/cgroup/pids.events",
		"/proc/sys/kernel/pid_max",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", path, err)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", path, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(b.String())
}
