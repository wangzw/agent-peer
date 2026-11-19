package syscontainer

import "testing"

// twoSuiteSubnets is the number of system subnets two suites that run on one
// host at the same time need. Each suite has about thirty top-level tests that
// start a container, and the allocator hands out one subnet per live container,
// so two suites together hold more than the old 32-slot pool.
const twoSuiteSubnets = 64

// TestSystemSubnetPoolServesTwoSuites requires the system pool to hand out
// enough distinct subnets for two suites running at the same time. The old pool
// had one /24 per slot and only 32 slots, so the 33rd allocation failed with
// "no free /24 left in [480, 511]" and a second concurrent suite broke the
// first one's tests.
func TestSystemSubnetPoolServesTwoSuites(t *testing.T) {
	seen := make(map[string]bool, twoSuiteSubnets)
	for i := 0; i < twoSuiteSubnets; i++ {
		subnet := allocSystemSubnet(t)
		if seen[subnet] {
			t.Fatalf("allocation %d returned %s twice", i+1, subnet)
		}
		seen[subnet] = true
	}
}
