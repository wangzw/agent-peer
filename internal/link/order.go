package link

import (
	"net"
	"time"
)

// orderCandidates sorts the candidate addresses of a dialer peer for one dial
// round. The last connected address comes first even when it is not among the
// candidates, then the addresses whose host is an IP literal on a local
// network, then the rest. Order within each group is the input order. The
// result contains no duplicate strings.
func orderCandidates(addrs []string, last string, local []*net.IPNet) []string {
	seen := make(map[string]struct{}, len(addrs)+1)
	out := make([]string, 0, len(addrs)+1)
	add := func(addr string) {
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}

	if last != "" {
		add(last)
	}

	sameNet := make([]string, 0, len(addrs))
	rest := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if isLocalAddress(addr, local) {
			sameNet = append(sameNet, addr)
		} else {
			rest = append(rest, addr)
		}
	}
	for _, addr := range sameNet {
		add(addr)
	}
	for _, addr := range rest {
		add(addr)
	}
	return out
}

// isLocalAddress reports whether addr is host:port with an IP literal host that
// belongs to one of local. Hosts that are names, malformed addresses and IPv6
// literals without brackets are not local.
func isLocalAddress(addr string, local []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range local {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// backoffDelay returns the delay after the given number of consecutive failed
// dial rounds. The delay starts at min and doubles after every failure, capped
// at max. A min above max returns min so the caller never busy-loops.
func backoffDelay(failures int, min, max time.Duration) time.Duration {
	if max < min || failures <= 0 {
		return min
	}
	delay := min << failures
	previous := min << (failures - 1)
	if delay <= 0 || delay < previous || delay > max {
		return max
	}
	return delay
}

// defaultLocalNets returns the interface networks of this host, skipping
// loopback addresses.
func defaultLocalNets() ([]*net.IPNet, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	nets := make([]*net.IPNet, 0, len(addrs))
	for _, addr := range addrs {
		network, ok := addr.(*net.IPNet)
		if !ok || network.IP.IsLoopback() {
			continue
		}
		nets = append(nets, network)
	}
	return nets, nil
}
