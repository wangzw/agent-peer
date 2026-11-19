// Package syscontainer runs the system-level end-to-end tests inside
// containers. This file carries no e2e build tag so that the pure evidence
// formatter below and its host-side test compile and run without one; the
// docker calls that feed it live in evidence_collect.go.
package syscontainer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// evidenceTailLines bounds each captured stream. When a systemd container does
// not boot, the interesting lines are the last ones before systemd gave up, and
// an unbounded dump would bury the rest of the test log.
const evidenceTailLines = 40

// containerState is the part of docker inspect's State that tells the boot
// failures apart: an OOM kill, a nonzero exit, or a container that never left
// the created state.
type containerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	OOMKilled  bool   `json:"OOMKilled"`
	ExitCode   int    `json:"ExitCode"`
	Error      string `json:"Error"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
}

// bootEvidence is the raw output collected when a systemd container does not
// reach running or degraded. Every field is command output except the host
// reads.
type bootEvidence struct {
	// State is docker inspect --format '{{json .State}}'.
	State string
	// Logs is the tail of docker logs.
	Logs string
	// SystemState is systemctl is-system-running (without --wait).
	SystemState string
	// Jobs is systemctl list-jobs: the pending jobs that keep the system in
	// the initializing state.
	Jobs string
	// Activating lists the units that are still activating, which a waiting
	// job does not show.
	Activating string
	// ProcessState is process 1's kernel wait channel and status. An idle
	// ep_poll with pending jobs differs from a process blocked in a syscall.
	ProcessState string
	// Processes is docker top, which shows what process 1 and the helpers are
	// doing while systemd has not settled.
	Processes string
	// Journal is the tail of journalctl -b, including the host kernel messages
	// the container's journald forwards from /dev/kmsg.
	Journal string
	// JournalSystemd is the tail of journalctl -b restricted to process 1, the
	// container's own systemd.
	JournalSystemd string
	// HostMemory is the first lines of /proc/meminfo.
	HostMemory string
	// HostPids is the host pids accounting.
	HostPids string
	// Containers is how many containers the daemon reported while the test
	// ran, so a failure can be tied to the concurrency it saw.
	Containers string
}

// parseContainerState decodes docker inspect's State JSON. ok is false when the
// input is not a JSON object, so the caller can show the daemon's error text
// instead of losing it to a parse failure.
func parseContainerState(raw string) (state containerState, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return containerState{}, false
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return containerState{}, false
	}
	return state, true
}

// formatBootEvidence renders the collected evidence for a test log. The State
// fields keep docker's own names so a reader can compare them with docker
// inspect output.
func formatBootEvidence(e bootEvidence) string {
	var b strings.Builder
	b.WriteString("systemd container boot evidence:")

	if state, ok := parseContainerState(e.State); ok {
		fmt.Fprintf(&b, "\n  State: Status=%s Running=%t OOMKilled=%t ExitCode=%d Error=%q StartedAt=%s FinishedAt=%s",
			state.Status, state.Running, state.OOMKilled, state.ExitCode, state.Error, state.StartedAt, state.FinishedAt)
	} else if text := strings.TrimSpace(e.State); text != "" {
		fmt.Fprintf(&b, "\n  State (raw): %s", text)
	} else {
		b.WriteString("\n  State (raw): (no output)")
	}

	writeEvidenceSection(&b, "docker logs", e.Logs)
	writeEvidenceSection(&b, "docker top", e.Processes)
	writeEvidenceSection(&b, "systemctl is-system-running", e.SystemState)
	writeEvidenceSection(&b, "systemctl list-jobs", e.Jobs)
	writeEvidenceSection(&b, "activating units", e.Activating)
	writeEvidenceSection(&b, "process 1 wchan/status", e.ProcessState)
	writeEvidenceSection(&b, "journalctl -b", e.Journal)
	writeEvidenceSection(&b, "journalctl -b _PID=1", e.JournalSystemd)
	writeEvidenceSection(&b, "host memory", e.HostMemory)
	writeEvidenceSection(&b, "host pids", e.HostPids)
	writeEvidenceSection(&b, "running containers", e.Containers)

	return b.String()
}

// writeEvidenceSection appends one indented section, bounded to the last
// evidenceTailLines lines.
func writeEvidenceSection(b *strings.Builder, name, text string) {
	fmt.Fprintf(b, "\n  %s:", name)
	tail := tailLines(text, evidenceTailLines)
	if tail == "" {
		b.WriteString(" (no output)")
		return
	}
	for _, line := range strings.Split(tail, "\n") {
		b.WriteString("\n    ")
		b.WriteString(line)
	}
}

// headLines keeps the first n lines of s.
func headLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
