package harness

import (
	"os"
	"testing"
)

// TestProjectNameCarriesOwner checks that a project name parses back to this
// process, so a cleanup can tell a live suite from stale residue.
func TestProjectNameCarriesOwner(t *testing.T) {
	current := currentOwner()
	if current.pid != os.Getpid() {
		t.Fatalf("currentOwner pid = %d, want %d", current.pid, os.Getpid())
	}
	name := projectName("TestProjectNameCarriesOwner")
	got, ok := parseOwner(name)
	if !ok || got != current {
		t.Fatalf("parseOwner(%q) = %+v, %v; want %+v", name, got, ok, current)
	}
	if ownerGone(name) {
		t.Fatalf("ownerGone(%q) = true; the owner is this live process", name)
	}
}

// TestOwnerGone checks the three cases a cleanup distinguishes: a live owner, a
// dead owner, and a name without the marker.
func TestOwnerGone(t *testing.T) {
	current := currentOwner()
	live := "ape2e-" + current.String() + "-test-abcdef"
	if ownerGone(live) {
		t.Fatalf("ownerGone(%q) = true; want false for the live owner", live)
	}
	if !ownerGone("ape2e-999999999-1-test-abcdef") {
		t.Errorf("ownerGone did not report the dead owner")
	}
	for _, name := range []string{
		"ape2e-legacy-abcdef",
		"ape2e12345",
		"not-a-project",
	} {
		if ownerGone(name) {
			t.Errorf("ownerGone(%q) = true; a name without a marker has an unknown owner", name)
		}
	}
}

// TestOwnerStartTimeMismatch checks that a reused PID with a different start
// time does not protect residue.
func TestOwnerStartTimeMismatch(t *testing.T) {
	current := currentOwner()
	if current.start == 0 {
		t.Skip("this platform does not report a process start time")
	}
	reused := owner{pid: current.pid, start: current.start + 1}
	if reused.alive() {
		t.Fatalf("owner %+v is alive; the start time does not match", reused)
	}
	if !ownerGone("ape2e-" + reused.String() + "-test-abcdef") {
		t.Fatalf("ownerGone did not reject the mismatched start time %+v", reused)
	}
}

// TestParseOwnerValue checks the standalone marker written into a subnet
// reservation.
func TestParseOwnerValue(t *testing.T) {
	current := currentOwner()
	got, ok := parseOwnerValue(current.String())
	if !ok || got != current {
		t.Fatalf("parseOwnerValue(%q) = %+v, %v; want %+v", current.String(), got, ok, current)
	}
	if _, ok := parseOwnerValue("garbage"); ok {
		t.Fatalf("parseOwnerValue accepted garbage")
	}
}
