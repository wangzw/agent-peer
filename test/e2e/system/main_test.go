//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

// TestMain forwards every test to a container through syscontainer.Main. The
// register-client role instead opens a Register stream directly, so that a
// test can observe the Broker's answer for another account's session.
func TestMain(m *testing.M) {
	if os.Getenv(registerClientRoleEnv) == registerClientRole {
		os.Exit(runRegisterClientRole())
	}
	os.Exit(syscontainer.Main(m))
}
