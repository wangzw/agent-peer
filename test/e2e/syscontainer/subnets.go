// Package syscontainer runs the system-level end-to-end tests inside
// containers. This file carries no e2e build tag so that the allocator's range
// and its host-side test compile and run without one.
package syscontainer

import (
	"net/netip"
	"testing"

	"github.com/wangzw/peers/test/e2e/harness"
)

const (
	// systemSubnetFirst and systemSubnetLast bound the /24s the container
	// network may use, as indices into 198.18.0.0/15. The harness routed
	// allocator stops at 479 and reserves this high-end block, so the two
	// allocators never hand out the same subnet.
	systemSubnetFirst = 480
	systemSubnetLast  = 511
	// systemSubnetBits is the size of one pool slot. Each reserved /24 is
	// divided into sixteen /28s, so the pool holds 512 slots: one suite starts
	// about thirty containers, and two suites running on one host at the same
	// time exceed the 32 /24s the pool held when it handed out whole /24s.
	// A /28 has sixteen addresses: one goes to the network, one to broadcast
	// and one to the gateway, which leaves thirteen container addresses. That
	// covers the one or two services a project starts today and leaves room
	// for a future two-container topology.
	systemSubnetBits = 28
)

// systemSubnetPrefixes returns every prefix allocSystemSubnet can hand out.
func systemSubnetPrefixes() []netip.Prefix {
	step := 1 << (32 - systemSubnetBits)
	out := make([]netip.Prefix, 0, (systemSubnetLast-systemSubnetFirst+1)*step)
	for index := systemSubnetFirst; index <= systemSubnetLast; index++ {
		for offset := 0; offset < 256; offset += step {
			out = append(out, systemSubnetPrefix(index, offset))
		}
	}
	return out
}

// systemSubnetPrefix formats the index-th /24 of 198.18.0.0/15 with offset
// added inside it. The index is the same one the harness routed allocator
// counts, which keeps the two ranges provably disjoint.
func systemSubnetPrefix(index, offset int) netip.Prefix {
	return netip.PrefixFrom(
		netip.AddrFrom4([4]byte{198, byte(18 + index/256), byte(index % 256), byte(offset)}),
		systemSubnetBits,
	)
}

// allocSystemSubnet reserves one subnet for one compose project and returns it
// as a CIDR. Docker refuses two live networks with overlapping subnets, and
// every system test starts its own project, so a subnet fixed in compose.yaml
// would make parallel tests fail. The reservation goes through the harness
// allocator, which locks the shared state directory and records the owner: a
// second suite running at the same time takes another slot instead of the same
// one. The range is disjoint from the harness routed allocator's.
func allocSystemSubnet(t *testing.T) string {
	t.Helper()

	prefix, err := harness.ReserveSubnetPrefix(t, systemSubnetPrefixes())
	if err != nil {
		t.Fatalf("syscontainer: reserving a system network subnet: %v", err)
	}
	return prefix.String()
}
