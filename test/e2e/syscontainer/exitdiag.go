// This file carries no e2e build tag so that the exit-status diagnostic below
// and its host-side test compile and run without one; the docker call that
// feeds it lives in syscontainer.go.
package syscontainer

import (
	"fmt"
	"strings"
	"time"

	"github.com/wangzw/peers/test/e2e/harness"
)

// killedExitCode is the exit status of a process killed by SIGKILL. A
// container returns it when the kernel or docker kills the process, most
// commonly because the kernel's OOM killer chose it. The container's own logs
// usually end before the kill, so the daemon's event stream is what explains
// it.
const killedExitCode = 137

// exitDiagnosticCommand returns the docker events query that lists the events
// of the project's owner label from started until now. The end is pinned with
// --until because docker events without it waits for new events and would hang
// the failure report; the events of the failed run are already recorded.
func exitDiagnosticCommand(owner string, started, now time.Time) []string {
	return []string{
		"events",
		"--filter", "label=" + harness.OwnerLabel + "=" + owner,
		"--since", started.UTC().Format(time.RFC3339Nano),
		"--until", now.UTC().Format(time.RFC3339Nano),
	}
}

// formatExitDiagnostic renders the events of a run that exited with
// killedExitCode. It returns "" for any other exit status, so a caller can
// append the result to every failure without checking the status twice.
func formatExitDiagnostic(code int, events string) string {
	if code != killedExitCode {
		return ""
	}
	events = strings.TrimSpace(events)
	if events == "" {
		events = "(no events)"
	}
	return fmt.Sprintf("the inner process ended with exit code %d; docker events for the project's owner label since it started:\n%s",
		code, events)
}

// collectExitDiagnostic runs the events query and formats its output. run is a
// parameter so a test can inject the daemon's answer instead of reaching it.
func collectExitDiagnostic(code int, run func(args ...string) string, owner string, started, now time.Time) string {
	if code != killedExitCode {
		return ""
	}
	return formatExitDiagnostic(code, run(exitDiagnosticCommand(owner, started, now)...))
}
