//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

// TestMain forwards every test to a container through syscontainer.Main.
func TestMain(m *testing.M) {
	os.Exit(syscontainer.Main(m))
}
