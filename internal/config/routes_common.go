package config

import "net/netip"

// addressSource used by core. The per-OS files provide defaultRouteAddresses.
type platformAddressSource struct{}

func (platformAddressSource) DefaultRouteAddresses() ([]netip.Addr, error) {
	return defaultRouteAddresses()
}

// Address classification shared by the platform-specific route enumerators.

// isIPv4LinkLocal reports whether addr is in 169.254.0.0/16.
func isIPv4LinkLocal(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	b := addr.As4()
	return b[0] == 169 && b[1] == 254
}

// isIPv6LinkLocal reports whether addr is in fe80::/10.
func isIPv6LinkLocal(addr netip.Addr) bool {
	if !addr.Is6() {
		return false
	}
	b := addr.As16()
	return b[0] == 0xfe && b[1]&0xc0 == 0x80
}

func dedupeAddrs(in []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(in))
	out := make([]netip.Addr, 0, len(in))
	for _, a := range in {
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}
