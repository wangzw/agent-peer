//go:build !agentpeer_testenv

package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	// releaseChildEnv marks the child process of TestTestEnvIgnoredInReleaseBuild.
	releaseChildEnv = "AGENT_PEER_TESTENV_RELEASE_CHILD"
	// releaseRoot and releaseHome are the values the child puts in the
	// environment. A build that installs the hooks would use them.
	releaseRoot = "/r"
	releaseHome = "/h"
)

// TestTestEnvIgnoredInReleaseBuild checks that a build without the
// agentpeer_testenv tag does not install the environment hooks. The hooks are
// installed by package init, so the environment has to be in place before the
// child starts: t.Setenv runs after init and therefore passes whether or not
// the hooks exist. The child re-executes this test binary with the selectors
// already set.
func TestTestEnvIgnoredInReleaseBuild(t *testing.T) {
	if os.Getenv(releaseChildEnv) == "1" {
		if got, want := runtimeDirPath(1000), "/tmp/agent-peer-1000"; got != want {
			t.Errorf("runtimeDirPath(1000) = %q, want %q", got, want)
		}
		got, err := homeDirSource()
		if err != nil {
			t.Fatalf("homeDirSource() error = %v", err)
		}
		if got == releaseHome {
			t.Errorf("homeDirSource() = %q, release build must ignore AGENT_PEER_TEST_HOME", got)
		}
		return
	}
	runReleaseChild(t, "TestTestEnvIgnoredInReleaseBuild")
}

// runReleaseChild runs name in a fresh process of this test binary and fails
// when that process fails. The explicit PASS line catches a -test.run pattern
// that matches nothing, which would otherwise exit successfully without
// checking anything.
func runReleaseChild(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
	cmd.Env = releaseChildEnvVars(
		releaseChildEnv+"=1",
		"AGENT_PEER_TEST_RUNTIME_ROOT="+releaseRoot,
		"AGENT_PEER_TEST_HOME="+releaseHome,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release-build child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("child did not run %s:\n%s", name, out)
	}
}

// releaseChildEnvVars returns the current environment with overrides applied.
// A key in overrides is removed before it is added again, so the child cannot
// inherit a second value for the same variable.
func releaseChildEnvVars(overrides ...string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, kv := range overrides {
		keys[strings.SplitN(kv, "=", 2)[0]] = true
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		if !keys[strings.SplitN(kv, "=", 2)[0]] {
			env = append(env, kv)
		}
	}
	return append(env, overrides...)
}
