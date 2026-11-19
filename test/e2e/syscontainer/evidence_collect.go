//go:build e2e

package syscontainer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// systemdEvidenceBound bounds the whole evidence collection. It is a separate
// budget that starts after systemdReadyBound is given up, so it does not extend
// the readiness bound; a hung diagnostic command loses its own text and the
// rest of the evidence is still collected.
const systemdEvidenceBound = 10 * time.Second

// logBootEvidence writes the state of the systemd container to the test log. It
// runs while the container still exists, before the deferred teardown removes
// it.
func (c *container) logBootEvidence() {
	c.t.Helper()
	c.t.Logf("syscontainer: %s", c.bootEvidence())
}

// bootEvidence collects the state that explains why systemd did not become
// ready. The whole collection shares systemdEvidenceBound, which starts after
// the readiness bound is given up and does not extend it. Every command's error
// text becomes part of the evidence, so a container that is already gone still
// leaves a record.
func (c *container) bootEvidence() string {
	ctx, cancel := context.WithTimeout(context.Background(), systemdEvidenceBound)
	defer cancel()
	run := func(args ...string) string { return c.docker(ctx, args...) }
	return collectBootEvidence(c.project, c.composeFile, run, readHostMemory, readHostPids)
}

// docker runs one docker command under ctx and returns its combined output. The
// error text is part of the result: a command that failed is evidence too.
func (c *container) docker(ctx context.Context, args ...string) string {
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
	return headLines(strings.TrimSpace(string(data)), 3)
}

// readHostPids reports the host pids accounting that a container kill for a
// process limit would show against. The root cgroup has no pids files of its
// own on a systemd host, so the system slice and the kernel limit are read as
// well.
func readHostPids() string {
	var b strings.Builder
	for _, path := range []string{
		"/proc/sys/kernel/pid_max",
		"/proc/loadavg",
		"/sys/fs/cgroup/pids.current",
		"/sys/fs/cgroup/pids.max",
		"/sys/fs/cgroup/pids.events",
		"/sys/fs/cgroup/system.slice/pids.current",
		"/sys/fs/cgroup/system.slice/pids.max",
		"/sys/fs/cgroup/system.slice/pids.events",
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
