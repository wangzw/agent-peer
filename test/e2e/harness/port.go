package harness

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// portStateDir holds one file per reserved loopback port, away from the
// runtime roots so the residue scan does not mistake it for a root. The files
// make the allocation cross-process: a second suite on the same host sees the
// first suite's live reservations and chooses other ports. Kernel allocation
// of port zero is sticky: a fresh process often gets 32769, so without the
// reservation two suites started together collide on their first addresses.
const (
	portStateDir = "/tmp/.ape2e-ports"
	portLockName = ".lock"
	// portScanFirst and portScanLast bound the ports the allocator probes.
	// Ports below the range are left to system services.
	portScanFirst = 20000
	portScanLast  = 65535
)

var portMu sync.Mutex

// ephemeralPortLow is the lower bound of the kernel's ephemeral port range,
// read once at startup. The allocator prefers ports below it: a reserved port
// inside the ephemeral range can be taken by an outbound connect before the
// Broker binds it.
var ephemeralPortLow = readEphemeralPortLow()

// readEphemeralPortLow returns the lower bound of
// /proc/sys/net/ipv4/ip_local_port_range, or zero when the file cannot be
// read.
func readEphemeralPortLow() int {
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	low, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	return low
}

// reservePort returns the loopback address whose port this owner now holds.
// The reservation lives until releasePorts runs or the owner is gone, so a
// second call in this process and a second suite on the same host both move to
// another port.
func reservePort(o owner) (string, int, error) {
	portMu.Lock()
	defer portMu.Unlock()

	unlock, err := lockPortState()
	if err != nil {
		return "", 0, err
	}
	defer unlock()

	taken, err := readPortReservations()
	if err != nil {
		return "", 0, err
	}

	for attempt := 0; attempt < 10; attempt++ {
		port, err := findFreePort(taken)
		if err != nil {
			return "", 0, err
		}
		if err := writePortReservation(port, o); err == nil {
			return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), port, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", 0, err
		}
		// Another process claimed the port without taking the lock; treat it
		// as taken and probe again.
		taken[port] = true
	}
	return "", 0, fmt.Errorf("reserving a loopback port: every candidate was claimed")
}

// findFreePort scans the preferred range first and then the rest for a port
// that is neither reserved nor in use.
func findFreePort(taken map[int]bool) (int, error) {
	for _, r := range portScanRanges(ephemeralPortLow) {
		for port := r.first; port <= r.last; port++ {
			if taken[port] {
				continue
			}
			if !portBindable(port) {
				continue
			}
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free loopback port in %d-%d", portScanFirst, portScanLast)
}

// portRange is one inclusive span of ports to scan.
type portRange struct {
	first, last int
}

// portScanRanges puts the ports below the kernel's ephemeral range first. A
// port there cannot be taken by an outbound connect, so the reservation stays
// free until the Broker binds it. When the low bound is unknown or leaves no
// room, the whole range is scanned in order.
func portScanRanges(low int) []portRange {
	if low <= 0 {
		return []portRange{{portScanFirst, portScanLast}}
	}
	var ranges []portRange
	if preferredLast := low - 1; preferredLast >= portScanFirst {
		if preferredLast > portScanLast {
			preferredLast = portScanLast
		}
		ranges = append(ranges, portRange{portScanFirst, preferredLast})
	}
	restFirst := low
	if restFirst < portScanFirst {
		restFirst = portScanFirst
	}
	if restFirst <= portScanLast {
		ranges = append(ranges, portRange{restFirst, portScanLast})
	}
	return ranges
}

// portBindable reports whether port can be bound on loopback.
func portBindable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	if err := listener.Close(); err != nil {
		return false
	}
	return true
}

// readPortReservations returns the ports whose owner is still alive and
// removes the files of owners that are gone. Owners are compared by their
// marker text so one liveness check serves every reservation.
func readPortReservations() (map[int]bool, error) {
	entries, err := os.ReadDir(portStateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[int]bool{}, nil
		}
		return nil, err
	}
	alive := map[string]bool{}
	taken := map[int]bool{}
	for _, entry := range entries {
		if entry.Name() == portLockName {
			continue
		}
		port, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(portStatePath(port))
		if err != nil {
			continue
		}
		raw := string(data)
		live, checked := alive[raw]
		if !checked {
			if o, ok := parseOwnerValue(raw); ok {
				live = o.alive()
			}
			alive[raw] = live
		}
		if live {
			taken[port] = true
			continue
		}
		_ = os.Remove(portStatePath(port))
	}
	return taken, nil
}

// writePortReservation records port under this owner's marker.
func writePortReservation(port int, o owner) error {
	path := portStatePath(port)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(o.String()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// releasePorts removes the reservations this owner wrote for ports.
func releasePorts(ports []int, o owner) {
	portMu.Lock()
	defer portMu.Unlock()
	unlock, err := lockPortState()
	if err != nil {
		return
	}
	defer unlock()
	for _, port := range ports {
		data, err := os.ReadFile(portStatePath(port))
		if err != nil {
			continue
		}
		if got, ok := parseOwnerValue(string(data)); ok && got == o {
			_ = os.Remove(portStatePath(port))
		}
	}
}

// cleanPortReservations removes reservation files whose owner is gone and
// leaves a live suite's ports reserved.
func cleanPortReservations(logf func(format string, args ...any)) {
	if _, err := os.Stat(portStateDir); err != nil {
		return
	}
	unlock, err := lockPortState()
	if err != nil {
		logf("cleanup: locking port reservations: %v", err)
		return
	}
	defer unlock()
	if _, err := readPortReservations(); err != nil {
		logf("cleanup: reading port reservations: %v", err)
	}
}

// lockPortState takes the cross-process lock and returns its release.
func lockPortState() (func(), error) {
	if err := os.MkdirAll(portStateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(portStateDir, portLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func portStatePath(port int) string {
	return filepath.Join(portStateDir, strconv.Itoa(port))
}
