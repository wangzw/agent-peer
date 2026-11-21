//go:build !peers_testenv

package update

import "testing"

// TestAPIBaseIgnoresTestOverrideInReleaseBuild proves that a build without the
// peers_testenv tag never reads PEERS_TEST_UPDATE_API, even when the
// variable is set. The goreleaser hook also checks that the literal variable
// name is absent from the release binary.
func TestAPIBaseIgnoresTestOverrideInReleaseBuild(t *testing.T) {
	t.Setenv("PEERS_TEST_UPDATE_API", "http://127.0.0.1:1")
	if got := APIBase(); got != DefaultAPI {
		t.Fatalf("APIBase() = %q, a release build must ignore the test override", got)
	}
}
