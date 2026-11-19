package syscontainer

import (
	"errors"
	"testing"
	"time"
)

// TestInterpretSystemdState checks the readiness decision, including the exit
// status 1 that systemctl returns for degraded despite the system being ready.
func TestInterpretSystemdState(t *testing.T) {
	cases := []struct {
		name      string
		output    string
		err       error
		wantState string
		wantReady bool
		wantGone  bool
	}{
		{
			name:      "running is ready",
			output:    "running\n",
			wantState: "running",
			wantReady: true,
		},
		{
			name:      "degraded exits 1 but is ready",
			output:    "degraded\n",
			err:       errors.New("exit status 1"),
			wantState: "degraded",
			wantReady: true,
		},
		{
			name:      "a state that is neither is not ready",
			output:    "initializing\n",
			wantState: `state "initializing"`,
		},
		{
			name:      "the killed probe leaves the error in the state",
			err:       errors.New("signal: killed"),
			wantState: "signal: killed: ",
		},
		{
			name:      "an exited container is reported as not running",
			output:    "service \"systemd\" is not running\n",
			err:       errors.New("exit status 1"),
			wantState: "exit status 1: service \"systemd\" is not running",
			wantGone:  true,
		},
	}
	for _, tc := range cases {
		got := interpretSystemdState(tc.output, tc.err)
		if got.ready != tc.wantReady {
			t.Errorf("%s: ready = %t, want %t", tc.name, got.ready, tc.wantReady)
		}
		if got.notRunning != tc.wantGone {
			t.Errorf("%s: notRunning = %t, want %t", tc.name, got.notRunning, tc.wantGone)
		}
		if got.state != tc.wantState {
			t.Errorf("%s: state = %q, want %q", tc.name, got.state, tc.wantState)
		}
	}
}

// TestShouldRebuildSystemd checks the retry decision: an exited container is
// recreated at once, a running but unready one only after the delay, and never
// more than the named number of rebuilds.
func TestShouldRebuildSystemd(t *testing.T) {
	cases := []struct {
		name     string
		poll     systemdBootPoll
		elapsed  time.Duration
		rebuilds int
		want     bool
	}{
		{
			name:     "exited container is rebuilt at once",
			poll:     systemdBootPoll{NotRunning: true},
			rebuilds: 0,
			want:     true,
		},
		{
			name:     "a boot still in progress is left alone",
			poll:     systemdBootPoll{},
			elapsed:  5 * time.Second,
			rebuilds: 0,
			want:     false,
		},
		{
			name:     "a stuck boot is rebuilt after the delay",
			poll:     systemdBootPoll{},
			elapsed:  systemdBootRebuildDelay,
			rebuilds: 0,
			want:     true,
		},
		{
			name:     "an exited container is not rebuilt twice",
			poll:     systemdBootPoll{NotRunning: true},
			rebuilds: systemdBootRebuilds,
			want:     false,
		},
		{
			name:     "a stuck boot is not rebuilt twice",
			poll:     systemdBootPoll{},
			elapsed:  time.Hour,
			rebuilds: systemdBootRebuilds,
			want:     false,
		},
	}
	for _, tc := range cases {
		if got := shouldRebuildSystemd(tc.poll, tc.elapsed, tc.rebuilds); got != tc.want {
			t.Errorf("%s: shouldRebuildSystemd(%+v, %s, %d) = %t, want %t",
				tc.name, tc.poll, tc.elapsed, tc.rebuilds, got, tc.want)
		}
	}
}
