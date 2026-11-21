package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// mainEntryEnv marks a re-execution of the test binary as the real entry
// point. TestMain checks it before running the tests, so the child goes
// through main exactly as the installed binary would.
const mainEntryEnv = "PEERS_TEST_MAIN_ENTRY"

// runMainEntryForTest is the child half of
// TestMainEntryTurnsAPanicIntoExitFailure. It registers a command that panics
// and runs the real entry point with that command. The trigger lives in the
// test binary only: no build of the product contains it.
func runMainEntryForTest() {
	addTestInternalCommands = func(internal *cobra.Command, _ *app) {
		internal.AddCommand(&cobra.Command{
			Use:                "panic-test",
			Hidden:             true,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, _ []string) error {
				panic("test-entry panic")
			},
		})
	}
	os.Args = []string{"peers", "internal", "panic-test"}
	main()
	// main exits through os.Exit, so this is only a compiler guard.
	os.Exit(exitFailure)
}

// TestMainEntryTurnsAPanicIntoExitFailure drives the compiled entry point, not
// runGuarded on its own: it fails when main wires the command straight to
// a.run and skips the guard. The Go runtime exits with status 2 on an
// unrecovered panic, and the update contract reserves 2 for "this binary has
// no post-update command", so an unwired guard would let the previous binary
// continue an update whose step crashed.
func TestMainEntryTurnsAPanicIntoExitFailure(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), mainEntryEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run the entry point: %v; stderr: %s", err, stderr.String())
	}
	if got := exitErr.ExitCode(); got != exitFailure {
		t.Fatalf("exit code = %d, want %d; stderr: %s", got, exitFailure, stderr.String())
	}
	if want := "peers: internal error: test-entry panic"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}
