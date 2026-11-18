package helper

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// testRoleHooks lets a platform-specific test file add roles to TestMain.
var testRoleHooks = map[string]func() int{}

// TestMain lets this test binary act as a real helper child. Tests that need a
// child spawn os.Args[0] with AGENT_PEER_TEST_HELPER_ROLE set; see
// testExecStart. It also runs the access-check body when invoked with the
// production argument list, so a test can start the real command path.
func TestMain(m *testing.M) {
	switch role := os.Getenv("AGENT_PEER_TEST_HELPER_ROLE"); role {
	case "account-helper":
		if err := RunAccountHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "access-check":
		if err := RunAccessCheck(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "":
		if isAccessCheckArgs(os.Args) {
			if err := RunAccessCheck(os.Stdin, os.Stdout); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
		os.Exit(m.Run())
	default:
		if hook := testRoleHooks[role]; hook != nil {
			os.Exit(hook())
		}
		os.Exit(2)
	}
}

// isAccessCheckArgs reports whether args are the production access-check
// command line.
func isAccessCheckArgs(args []string) bool {
	return len(args) == 3 && args[1] == "internal" && args[2] == "access-check"
}

// testExecStart returns a Start function for PoolOptions or AccessChecker that
// runs the current test binary in the given role. The child gets a minimal
// environment and no credential change, so it runs as the test process.
func testExecStart(t *testing.T, role string) func(ctx context.Context, acct Account) (*Process, error) {
	t.Helper()
	home := t.TempDir()
	path := os.Getenv("PATH")
	return func(ctx context.Context, acct Account) (*Process, error) {
		cmd := exec.Command(os.Args[0])
		cmd.Env = []string{
			"AGENT_PEER_TEST_HELPER_ROLE=" + role,
			"HOME=" + home,
			"PATH=" + path,
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return &Process{
			Stdin:  stdin,
			Stdout: stdout,
			Wait:   cmd.Wait,
			Kill:   cmd.Process.Kill,
		}, nil
	}
}
