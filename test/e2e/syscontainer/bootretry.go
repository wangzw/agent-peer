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
	// systemdBootRebuilds bounds how many times a systemd container is
	// recreated after it fails to finish booting. The evidence from the full
	// gate under JOBS=32 is that a systemd container occasionally does not
	// boot, in one of two ways: PID 1 exits with status 255 about a second
	// after the container starts, or systemd stays in the initializing state
	// with every job in systemctl list-jobs still waiting while PID 1 is alive
	// and idle. Neither is an out-of-memory or pid-limit kill (OOMKilled=false,
	// the host had more than 400 GB free and its pid usage stayed far below the
	// limit), and a healthy boot of the same image takes under a second. A
	// fresh container therefore recovers the same test, and one rebuild is
	// enough; repeating it would only lengthen a failure that is not transient.
	systemdBootRebuilds = 1

	// systemdBootRebuildDelay is how long a running but unready systemd
	// container is left before it is recreated. A healthy boot finishes in well
	// under a second, and the observed stall never left the initializing state
	// within the ninety-second bound, so fifteen seconds separates a slow boot
	// from a stuck one with room to spare while leaving most of the bound for
	// the rebuilt container. A container that has already exited is recreated
	// at once instead of waiting.
	systemdBootRebuildDelay = 15 * time.Second
)

// systemdBootPoll is one observation of a systemd container during boot.
type systemdBootPoll struct {
	// NotRunning reports that the container itself is gone, so no amount of
	// waiting can help it.
	NotRunning bool
}

// systemdStateResult is one interpreted readiness probe.
type systemdStateResult struct {
	// state is the text reported to the test when the probe is not ready.
	state string
	// ready reports that systemd reached running or degraded.
	ready bool
	// notRunning reports that the container's process 1 is gone.
	notRunning bool
}

// interpretSystemdState turns one is-system-running result into a readiness
// decision. The text decides, not the exit code: systemctl exits 1 for degraded
// although a degraded system is ready for the tests. An error whose output does
// not name a settled state is reported with the error text.
func interpretSystemdState(output string, err error) systemdStateResult {
	text := strings.TrimSpace(output)
	if text == "running" || text == "degraded" {
		return systemdStateResult{state: text, ready: true}
	}
	if err != nil {
		return systemdStateResult{
			state:      fmt.Sprintf("%v: %s", err, text),
			notRunning: strings.Contains(text, "is not running"),
		}
	}
	return systemdStateResult{state: fmt.Sprintf("state %q", text)}
}

// shouldRebuildSystemd reports whether a systemd container that is not ready
// should be recreated now. rebuilds is the number of rebuilds already done and
// elapsed is the time spent waiting since the container was started.
func shouldRebuildSystemd(poll systemdBootPoll, elapsed time.Duration, rebuilds int) bool {
	if rebuilds >= systemdBootRebuilds {
		return false
	}
	if poll.NotRunning {
		return true
	}
	return elapsed >= systemdBootRebuildDelay
}
