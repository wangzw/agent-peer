//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/syscontainer"
)

// TestMain forwards every test to a container through syscontainer.Main. The
// register-client role instead opens a Register stream directly, so that a
// test can observe the Broker's answer for another account's session. The
// status-client role reads one agent ID from the Broker as the account, which
// the account's session registry permits and root does not.
func TestMain(m *testing.M) {
	switch os.Getenv(registerClientRoleEnv) {
	case registerClientRole:
		os.Exit(runRegisterClientRole())
	case listBrokersRole:
		os.Exit(runListBrokersRole())
	case forgeAccessCheckRole:
		os.Exit(runForgeAccessCheckRole())
	case statusClientRole:
		os.Exit(runStatusClientRole())
	case updateStatusClientRole:
		os.Exit(runUpdateStatusClientRole())
	}
	os.Exit(syscontainer.Main(m))
}
