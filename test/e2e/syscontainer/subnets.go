// Package syscontainer runs the system-level end-to-end tests inside
// containers. This file carries no e2e build tag so that the allocator's range
// and its host-side test compile and run without one.
package syscontainer

import "fmt"

const (
	// systemSubnetFirst and systemSubnetLast bound the /24s the container
	// network may use, as indices into 198.18.0.0/15. The harness routed
	// allocator stops at 479 and reserves this high-end block, so the two
	// allocators never hand out the same subnet.
	systemSubnetFirst = 480
	systemSubnetLast  = 511
)

// systemSubnetPrefixes returns every CIDR allocSystemSubnet can hand out.
func systemSubnetPrefixes() []string {
	out := make([]string, 0, systemSubnetLast-systemSubnetFirst+1)
	for index := systemSubnetFirst; index <= systemSubnetLast; index++ {
		out = append(out, systemSubnetCIDR(index))
	}
	return out
}

// systemSubnetCIDR formats the index-th /24 of 198.18.0.0/15 as a CIDR. The
// index is the same one the harness reservation records in its shared state
// directory.
func systemSubnetCIDR(index int) string {
	return fmt.Sprintf("198.%d.%d.0/24", 18+index/256, index%256)
}
