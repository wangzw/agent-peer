package harness

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

// The routed topologies pin every IPv4 subnet inside 198.18.0.0/15. Docker
// keeps neither its default bridge (172.17.0.0/16) nor the pool it allocates
// from (172.16.0.0/12 and 192.168.0.0/16) in that range, and this host is a
// k8s node whose own networks (10.12.72.0/24, 10.244.0.0/16, 172.17.0.0/16 and
// 10.96.0.0/12) must never be captured by a bridge route.
const (
	// routedSubnetSkip reserves the first eight /24s for the compose files'
	// defaults, so a manual "docker compose up" never takes a block the
	// allocator hands out.
	routedSubnetSkip   = 8
	routedSubnetStride = 3
	// routedSubnetCount is the number of /24s in 198.18.0.0/15.
	routedSubnetCount = 512
	// routedSubnetLimit stops the routed allocator before the last 32 /24s,
	// which are reserved for the system container network. Both allocators
	// stay inside 198.18.0.0/15, so neither can capture traffic for a public
	// address.
	routedSubnetLimit = routedSubnetCount - 32
	// subnetStateDir holds one file per reserved /24, away from the runtime
	// roots so the residue scan does not mistake it for a root.
	subnetStateDir = "/tmp/.ape2e-subnets"
	subnetLockName = ".lock"
)

var subnetMu sync.Mutex

// RoutedSubnetPrefixes returns every /24 the routed-topology allocator can
// hand out. It exists so that another allocator can prove its range never
// intersects this one: two live docker networks with overlapping subnets make
// docker refuse the second network.
func RoutedSubnetPrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, routedSubnetLimit-routedSubnetSkip)
	for first := routedSubnetSkip; first+2 < routedSubnetLimit; first += routedSubnetStride {
		out = append(out, routedSubnetPrefix(first), routedSubnetPrefix(first+1), routedSubnetPrefix(first+2))
	}
	return out
}

// allocRoutedSubnets reserves three consecutive /24s in 198.18.0.0/15 for this
// test. The reservation is cross-process: a second suite running at the same
// time sees the live reservation and takes the next block. A reservation whose
// owner is gone is reclaimed, so a killed run does not block the next one.
func allocRoutedSubnets(t *testing.T) [3]string {
	t.Helper()
	o := currentOwner()
	indices, err := reserveSubnetBlock(o)
	if err != nil {
		t.Fatalf("harness: reserving routed subnets: %v", err)
	}
	t.Cleanup(func() { releaseSubnetBlock(indices, o) })
	return [3]string{routedSubnet(indices[0]), routedSubnet(indices[1]), routedSubnet(indices[2])}
}

// reserveSubnetBlock finds three consecutive free indices under the state lock
// and writes this owner's marker to each.
func reserveSubnetBlock(o owner) ([3]int, error) {
	subnetMu.Lock()
	defer subnetMu.Unlock()

	unlock, err := lockSubnetState()
	if err != nil {
		return [3]int{}, err
	}
	defer unlock()

	taken, err := readSubnetReservations()
	if err != nil {
		return [3]int{}, err
	}
	for index := routedSubnetSkip; index+2 < routedSubnetLimit; index += routedSubnetStride {
		if taken[index] || taken[index+1] || taken[index+2] {
			continue
		}
		for i := index; i < index+3; i++ {
			if err := os.WriteFile(subnetStatePath(i), []byte(o.String()), 0o600); err != nil {
				return [3]int{}, err
			}
		}
		return [3]int{index, index + 1, index + 2}, nil
	}
	return [3]int{}, fmt.Errorf("no free /24 block left in 198.18.0.0/15")
}

// readSubnetReservations returns the indices whose owner is still alive and
// removes the files of owners that are gone.
func readSubnetReservations() (map[int]bool, error) {
	entries, err := os.ReadDir(subnetStateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[int]bool{}, nil
		}
		return nil, err
	}
	taken := map[int]bool{}
	for _, entry := range entries {
		if entry.Name() == subnetLockName {
			continue
		}
		index, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(subnetStatePath(index))
		if err != nil {
			continue
		}
		if o, ok := parseOwnerValue(string(data)); ok && o.alive() {
			taken[index] = true
			continue
		}
		_ = os.Remove(subnetStatePath(index))
	}
	return taken, nil
}

// cleanSubnetReservations removes reservation files whose owner is gone and
// leaves a live suite's blocks reserved.
func cleanSubnetReservations(logf func(format string, args ...any)) {
	if _, err := os.Stat(subnetStateDir); err != nil {
		return
	}
	unlock, err := lockSubnetState()
	if err != nil {
		logf("cleanup: locking subnet reservations: %v", err)
		return
	}
	defer unlock()
	if _, err := readSubnetReservations(); err != nil {
		logf("cleanup: reading subnet reservations: %v", err)
	}
}

// releaseSubnetBlock removes the reservations this owner wrote.
func releaseSubnetBlock(indices [3]int, o owner) {
	subnetMu.Lock()
	defer subnetMu.Unlock()
	unlock, err := lockSubnetState()
	if err != nil {
		return
	}
	defer unlock()
	for _, index := range indices {
		data, err := os.ReadFile(subnetStatePath(index))
		if err != nil {
			continue
		}
		if got, ok := parseOwnerValue(string(data)); ok && got == o {
			_ = os.Remove(subnetStatePath(index))
		}
	}
}

// lockSubnetState takes the cross-process lock and returns its release.
func lockSubnetState() (func(), error) {
	if err := os.MkdirAll(subnetStateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(subnetStateDir, subnetLockName), os.O_CREATE|os.O_RDWR, 0o600)
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

// routedSubnet formats the index-th /24 of 198.18.0.0/15 as a CIDR.
func routedSubnet(index int) string {
	return routedSubnetPrefix(index).String()
}

// routedSubnetPrefix returns the index-th /24 of 198.18.0.0/15 as a prefix.
func routedSubnetPrefix(index int) netip.Prefix {
	return netip.PrefixFrom(
		netip.AddrFrom4([4]byte{198, byte(18 + index/256), byte(index % 256), 0}),
		24,
	)
}

func subnetStatePath(index int) string {
	return filepath.Join(subnetStateDir, strconv.Itoa(index))
}
