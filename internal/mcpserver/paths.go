package mcpserver

import (
	"path/filepath"
	"runtime"
	"strconv"
)

// runtimeDir returns the runtime directory of the account euid. It is the seam
// the peers_testenv build replaces (cmd/peers unit adds
// internal/mcpserver/runtimeroot_testenv.go); production code never replaces it.
//
// The runtime directory does not depend on any environment variable. The
// account is taken by effective uid, matching the path table in
// design-config.md: the system-level Broker runs as root, every other account
// gets its own directory under /tmp.
var runtimeDir = func(euid int) string {
	if euid == 0 {
		return systemRuntimeDir(runtime.GOOS)
	}
	return "/tmp/peers-" + strconv.Itoa(euid)
}

// systemRuntimeDir returns the runtime directory of the root account on goos.
// The platform suffix is passed in rather than read from runtime.GOOS so the
// rule is testable on every host.
func systemRuntimeDir(goos string) string {
	if goos == "darwin" {
		return "/var/run/peers"
	}
	return "/run/peers"
}

// userSocketPath is the local socket of the Broker that runs as the account euid.
func userSocketPath(euid int) string {
	return filepath.Join(runtimeDir(euid), "broker.sock")
}

// systemSocketPath is the local socket of the system-level Broker, which runs as root.
func systemSocketPath() string {
	return filepath.Join(runtimeDir(0), "broker.sock")
}
