//go:build !agentpeer_testenv

package update

import "testing"

// TestAPIBaseIgnoresTestOverrideInReleaseBuild proves that a build without the
// agentpeer_testenv tag never reads AGENT_PEER_TEST_UPDATE_API, even when the
// variable is set. The goreleaser hook also checks that the literal variable
// name is absent from the release binary.
func TestAPIBaseIgnoresTestOverrideInReleaseBuild(t *testing.T) {
	t.Setenv("AGENT_PEER_TEST_UPDATE_API", "http://127.0.0.1:1")
	if got := APIBase(); got != DefaultAPI {
		t.Fatalf("APIBase() = %q, a release build must ignore the test override", got)
	}
}
