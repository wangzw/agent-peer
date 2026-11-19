package syscontainer

import (
	"fmt"
	"strings"
	"testing"
)

// TestFormatBootEvidence checks that the failure text carries the inspect State
// fields and the last lines of the captured streams, which is what turns a
// future boot failure into a diagnosis.
func TestFormatBootEvidence(t *testing.T) {
	var logs strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&logs, "docker log line %d\n", i)
	}
	var journal strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&journal, "journal line %d\n", i)
	}

	got := formatBootEvidence(bootEvidence{
		State:          `{"Status":"exited","Running":false,"OOMKilled":true,"ExitCode":137,"Error":"","StartedAt":"2026-11-19T01:00:00Z","FinishedAt":"2026-11-19T01:00:05Z"}`,
		Logs:           logs.String(),
		Processes:      "PID                 USER                TIME                COMMAND\n1 root 0:00 /sbin/init",
		SystemState:    "offline",
		Jobs:           "JOB UNIT TYPE STATE\n42 getty@tty1.service start running",
		Activating:     "dbus.service loaded activating start",
		ProcessState:   "ep_poll\nState:\tS (sleeping)\nThreads:\t1",
		Journal:        journal.String(),
		HostMemory:     "MemTotal:       527843948 kB\nMemFree:        415000000 kB\nMemAvailable:   490000000 kB",
		HostPids:       "/sys/fs/cgroup/pids.current: 812",
		Containers:     "9 running (docker ps -q)",
		JournalSystemd: "systemd[1]: Startup finished in 1s.",
	})

	for _, want := range []string{
		"Status=exited",
		"Running=false",
		"OOMKilled=true",
		"ExitCode=137",
		"docker log line 99",
		"journal line 99",
		"1 root 0:00 /sbin/init",
		"offline",
		"42 getty@tty1.service start running",
		"dbus.service loaded activating start",
		"ep_poll",
		"systemd[1]: Startup finished in 1s.",
		"MemAvailable:   490000000 kB",
		"/sys/fs/cgroup/pids.current: 812",
		"9 running (docker ps -q)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatBootEvidence output does not contain %q:\n%s", want, got)
		}
	}

	// The dump is bounded: the earliest lines of a long stream must not be
	// carried into the log.
	for _, unwanted := range []string{
		"docker log line 0\n",
		fmt.Sprintf("docker log line %d\n", 100-evidenceTailLines-1),
		"journal line 0\n",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("formatBootEvidence output contains %q beyond the tail bound:\n%s", unwanted, got)
		}
	}
}

// TestFormatBootEvidenceRawState checks that a daemon error that is not JSON is
// preserved instead of being dropped by the parse.
func TestFormatBootEvidenceRawState(t *testing.T) {
	const raw = "Error response from daemon: No such container: deadbeef"
	got := formatBootEvidence(bootEvidence{State: raw})
	if !strings.Contains(got, raw) {
		t.Errorf("formatBootEvidence output does not carry the raw State text:\n%s", got)
	}
}

// TestHeadLines checks the head bound the host reads use.
func TestHeadLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than bound", "a\nb", 3, "a\nb"},
		{"exactly the bound", "a\nb\nc", 3, "a\nb\nc"},
		{"longer than bound", "a\nb\nc\nd", 2, "a\nb"},
		{"trailing newline", "a\nb\n", 2, "a\nb"},
		{"empty", "", 2, ""},
		{"no lines requested", "a\n", 0, ""},
	}
	for _, tc := range cases {
		if got := headLines(tc.in, tc.n); got != tc.want {
			t.Errorf("%s: headLines(%q, %d) = %q, want %q", tc.name, tc.in, tc.n, got, tc.want)
		}
	}
}

// TestTailLines checks the bound used by every evidence stream.
func TestTailLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than bound", "a\nb", 3, "a\nb"},
		{"exactly the bound", "a\nb\nc", 3, "a\nb\nc"},
		{"longer than bound", "a\nb\nc\nd", 2, "c\nd"},
		{"trailing newline", "a\nb\n", 2, "a\nb"},
		{"empty", "", 2, ""},
		{"no lines requested", "a\n", 0, ""},
	}
	for _, tc := range cases {
		if got := tailLines(tc.in, tc.n); got != tc.want {
			t.Errorf("%s: tailLines(%q, %d) = %q, want %q", tc.name, tc.in, tc.n, got, tc.want)
		}
	}
}
