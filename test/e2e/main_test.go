//go:build e2e

package e2e

import (
	"flag"
	"log"
	"os"
	"testing"

	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// TestMain runs the end-to-end tests. The container image is built lazily by
// harness.Image, so a scenario that runs only on the test host never needs
// docker.
//
// The process go test starts does not run the tests itself. It starts a child
// copy with the same flags, waits for it, and cleans residue before and after.
// The child must be separate because a go test timeout panics without
// unwinding: a cleanup deferred in the test process would never run, while the
// waiting parent still cleans before go test returns. The cleanup only removes
// projects, processes and roots whose owner process is gone, so a suite running
// in parallel keeps its own.
func TestMain(m *testing.M) {
	flag.Parse()
	if harness.IsSupervisorChild() {
		os.Exit(m.Run())
	}

	harness.CleanResidue(log.Printf)
	code := harness.RunTestProcess(log.Printf, os.Args[1:])
	harness.CleanResidue(log.Printf)
	os.Exit(code)
}
