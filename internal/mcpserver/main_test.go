package mcpserver

import (
	"os"
	"testing"
)

// testRoles holds the entry points of the test double executables. The package
// may only have one TestMain, and both the launcher and the process entry tests
// re-execute the test binary, so each test file registers its roles here from
// init.
var testRoles = map[string]func() int{}

func TestMain(m *testing.M) {
	if role := os.Getenv("AGENT_PEER_TEST_ROLE"); role != "" {
		run, ok := testRoles[role]
		if !ok {
			os.Exit(2)
		}
		os.Exit(run())
	}
	os.Exit(m.Run())
}
