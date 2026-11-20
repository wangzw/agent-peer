package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// MainPID returns the pid the service manager reports for the installed
// agent-peer service at this deployment level, and whether it reports a running
// one. It asks the same question as the install-time check (serviceMainPID), so
// "started by the service" means the same thing in both places: on Linux
// "systemctl [--user] show -p MainPID", on macOS the pid in "launchctl print".
//
// A missing unit, a stopped service, a failed query and an unsupported platform
// all report false. Callers must compare the pid with their own before treating
// the process as service-managed: a unit that is running a different process
// says nothing about this one.
func MainPID(system bool) (int, bool) {
	return serviceMainPID(Options{
		System: system,
		GOOS:   runtime.GOOS,
		UID:    os.Getuid(),
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
	}, serviceDomain(system, os.Getuid()))
}

// serviceDomain names the launchd domain a service of this level is loaded in.
// Linux ignores the domain; the systemd query picks the unit by level instead.
func serviceDomain(system bool, uid int) string {
	if system {
		return "system"
	}
	return fmt.Sprintf("gui/%d", uid)
}
