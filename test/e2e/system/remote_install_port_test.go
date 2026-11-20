//go:build e2e

package e2e

import "testing"

// TestRemoteInstallServeTwoSuites reproduces the collision between two e2e
// suites on one host. Both suites run the same test, so a port derived from
// the test name is identical in both and the second fake release service fails
// to bind with "address already in use". The service must instead take a port
// the kernel assigns, which the test then hands to the container.
func TestRemoteInstallServeTwoSuites(t *testing.T) {
	remoteInstallServe(t, remoteInstallReleaseOptions{})
	remoteInstallServe(t, remoteInstallReleaseOptions{})
}
