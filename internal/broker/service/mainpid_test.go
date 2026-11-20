package service

import "testing"

// TestServiceDomain pins the launchd domain of each level. The system daemon
// lives in the system domain; a user agent lives in gui/<uid>, the domain the
// install path bootstraps it into.
func TestServiceDomain(t *testing.T) {
	if got := serviceDomain(true, 501); got != "system" {
		t.Errorf("serviceDomain(system) = %q, want system", got)
	}
	if got := serviceDomain(false, 501); got != "gui/501" {
		t.Errorf("serviceDomain(user) = %q, want gui/501", got)
	}
}
