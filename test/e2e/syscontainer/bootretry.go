// Package syscontainer runs the system-level end-to-end tests inside
// containers. This file carries no e2e build tag so that the retry decision
// below and its host-side test compile and run without one; the docker calls
// that apply it live in syscontainer.go.
package syscontainer

import (
	"fmt"
	"strings"
	"time"
)

const (
	// systemdBootRebuildDelay is how long a container that is still
	// initializing is left before it is rebuilt. The stall becomes observable
	// only when the wait probe's own bound expires, so this value is also that
	// probe's bound (systemdProbeBound in syscontainer.go); the rebuilt
	// container keeps the rest of the readiness bound.
	systemdBootRebuildDelay = 10 * time.Second

	// systemdBootRebuilds bounds how many times a systemd container is
	// recreated after it fails to finish booting, and limits the rebuild to the
	// two failure modes observed under the full gate: PID 1 exits with status
	// 255 about a second after the container starts and leaves docker logs
	// empty, or is-system-running --wait times out while the state is still
	// initializing (systemctl list-jobs then shows every job waiting, with PID 1
	// alive and idle). Neither is an out-of-memory or pid-limit kill
	// (OOMKilled=false, the host had more than 400 GB free and its pid usage
	// stayed far below the limit), and a healthy boot takes under a second, so
	// a fresh container recovers the same test. Any other failure, such as a
	// different exit code or another systemd state, is a real defect and fails
	// without a rebuild.
	systemdBootRebuilds = 1
)

// systemdBootObservation is what the wait knows about one failed readiness
// probe when it decides about a rebuild.
type systemdBootObservation struct {
	// exited reports that compose could not exec because process 1 is gone.
	exited bool
	// exitCode and logsEmpty describe the exited container.
	exitCode  int
	logsEmpty bool
	// probeTimedOut reports that is-system-running --wait used its whole bound.
	probeTimedOut bool
	// state is the non-blocking is-system-running text taken after a timeout.
	state string
	// elapsed is the time spent since the container was started.
	elapsed time.Duration
	// rebuilds is the number of rebuilds already done.
	rebuilds int
}

// shouldRebuildSystemd reports whether the observation is one of the two
// observed boot failures and a rebuild is still allowed.
func shouldRebuildSystemd(o systemdBootObservation) bool {
	if o.rebuilds >= systemdBootRebuilds {
		return false
	}
	if o.exited {
		return o.exitCode == 255 && o.logsEmpty
	}
	return o.probeTimedOut && o.state == "initializing" && o.elapsed >= systemdBootRebuildDelay
}

// systemdProbeResult is one interpreted is-system-running --wait result.
type systemdProbeResult struct {
	// state is the text reported to the test when the probe is not ready.
	state string
	// ready reports that systemd reached running or degraded.
	ready bool
	// exited reports that compose could not exec because process 1 is gone.
	exited bool
	// timedOut reports that the probe used its own bound.
	timedOut bool
}

// interpretSystemdState turns one is-system-running result into a probe result.
// The text decides readiness, not the exit code: systemctl exits 1 for degraded
// although a degraded system is ready for the tests. A probe killed by its own
// bound is a timeout, which the caller confirms and eventually reads as the
// initializing stall; an error whose output does not name a settled state is
// reported with the error text.
func interpretSystemdState(output string, err error, timedOut bool) systemdProbeResult {
	text := strings.TrimSpace(output)
	if text == "running" || text == "degraded" {
		return systemdProbeResult{state: text, ready: true}
	}
	if timedOut {
		return systemdProbeResult{timedOut: true}
	}
	if err != nil {
		return systemdProbeResult{
			state:  fmt.Sprintf("%v: %s", err, text),
			exited: strings.Contains(text, "is not running"),
		}
	}
	return systemdProbeResult{state: fmt.Sprintf("state %q", text)}
}
