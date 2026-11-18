//go:build !linux && !darwin

package config

import "net/netip"

// defaultRouteAddresses returns no addresses on platforms that are not
// release targets.
func defaultRouteAddresses() ([]netip.Addr, error) {
	return nil, nil
}
