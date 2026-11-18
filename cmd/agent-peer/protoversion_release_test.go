//go:build !agentpeer_testenv

package main

import "testing"

// TestProtocolOverridesIgnoredInReleaseBuild checks that a build without the
// agentpeer_testenv tag keeps the production seam, which returns 0 for both
// versions without reading the environment.
func TestProtocolOverridesIgnoredInReleaseBuild(t *testing.T) {
	t.Setenv("AGENT_PEER_TEST_LOCAL_PROTOCOL_VERSION", "7")
	t.Setenv("AGENT_PEER_TEST_LINK_PROTOCOL_VERSION", "7")

	local, link := protocolOverrides()
	if local != 0 || link != 0 {
		t.Fatalf("protocolOverrides() = (%d, %d), release build must return (0, 0)", local, link)
	}
}
