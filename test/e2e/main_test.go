//go:build e2e

package e2e

import (
	"flag"
	"os"
	"testing"
)

// TestMain runs the end-to-end tests. The container image is built lazily by
// harness.Image, so a scenario that runs only on the test host never needs
// docker.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}
