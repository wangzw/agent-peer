//go:build !agentpeer_testenv

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// releaseOverrideChildEnv marks the re-executed test binary. The environment
// variables that ask for protocol overrides are set before the child starts, so
// its release seam sees them from process start rather than through t.Setenv in
// the test process.
const releaseOverrideChildEnv = "AGENT_PEER_TEST_PROTOCOL_OVERRIDE_CHILD"

// TestProtocolOverridesIgnoredInReleaseBuild checks that a build without the
// agentpeer_testenv tag keeps the production seam. The check runs in a child
// process of the same test binary so the environment is fixed before the
// process starts and the test process keeps its own environment.
func TestProtocolOverridesIgnoredInReleaseBuild(t *testing.T) {
	if os.Getenv(releaseOverrideChildEnv) == "1" {
		local, link := protocolOverrides()
		if local != 0 || link != 0 {
			t.Fatalf("protocolOverrides() = (%d, %d), release build must return (0, 0)", local, link)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestProtocolOverridesIgnoredInReleaseBuild$")
	cmd.Env = releaseChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the release build read a protocol override from the environment: %v\n%s", err, out)
	}
}

// releaseChildEnv is the parent environment with the override variables set to
// 7 and the child marker added. The test process's own values are dropped so a
// caller that already exported them cannot decide what the child sees.
func releaseChildEnv() []string {
	dropped := []string{
		releaseOverrideChildEnv + "=",
		"AGENT_PEER_TEST_LOCAL_PROTOCOL_VERSION=",
		"AGENT_PEER_TEST_LINK_PROTOCOL_VERSION=",
	}
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		skip := false
		for _, prefix := range dropped {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			env = append(env, entry)
		}
	}
	return append(env,
		releaseOverrideChildEnv+"=1",
		"AGENT_PEER_TEST_LOCAL_PROTOCOL_VERSION=7",
		"AGENT_PEER_TEST_LINK_PROTOCOL_VERSION=7",
	)
}
