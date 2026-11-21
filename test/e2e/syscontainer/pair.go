// Package syscontainer runs the system end-to-end tests inside
// containers. This file carries no e2e build tag so that the role parsing and
// the shared-directory exchange compile and run on the host without one.
package syscontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/peers/test/e2e/harness"
)

const (
	// PairRoleEnv carries the role into a container that is part of a pair.
	// RunPairTest sets it to "A" for hostA and "B" for hostB.
	PairRoleEnv = "PEERS_E2E_PAIR_ROLE"

	// pairSharedEnv carries the shared directory path into a pair container.
	// The value is the container path, not the host path.
	pairSharedEnv = "PEERS_E2E_PAIR_SHARED"

	// PairSharedMount is the container path test/e2e/compose/system/compose.yaml
	// mounts the pair volume at. The compose file fixes the mount, so
	// PairOptions.SharedDir must equal it: any other value names a directory
	// neither host writes, and the exchange would fail only when a wait expires.
	PairSharedMount = "/shared"
)

// Role is the host a pair test process runs on.
type Role string

const (
	// RoleA is host A, which runs the system Broker and accepts the pairing.
	RoleA Role = "A"
	// RoleB is host B, which runs its own Broker and dials host A.
	RoleB Role = "B"
)

// PairOptions configures a test that runs on two hosts. TestMain passes one to
// RunPair and the test passes the same value to RunPairTest.
type PairOptions struct {
	// SharedDir is the directory path mounted at the same location in both
	// containers. The two roles exchange pairing files and completion markers
	// there. It must equal PairSharedMount; the host does not create it, the
	// compose volume provides it.
	SharedDir string
	// ExtraEnv adds NAME=value entries to both container runs. A pair that
	// needs an address the host chose at run time puts it here.
	ExtraEnv []string
}

// PairRole returns the role this process runs as, or "" when it is not part of
// a pair. A pair test calls it to select its half of the scenario.
func PairRole() Role {
	role := Role(os.Getenv(PairRoleEnv))
	switch role {
	case RoleA, RoleB:
		return role
	}
	return ""
}

// InPair reports whether this process runs as one host of a pair.
func InPair() bool {
	return PairRole() != ""
}

// PairSharedDir returns the directory the two pair containers share, or "" when
// this process is not part of a pair.
func PairSharedDir() string {
	return os.Getenv(pairSharedEnv)
}

// peerRole returns the other role of the pair.
func peerRole(role Role) Role {
	if role == RoleA {
		return RoleB
	}
	return RoleA
}

// pairFailureName is the file a host writes when its test fails, so the other
// host stops waiting instead of running out its own bound.
func pairFailureName(role Role) string {
	return "failed-" + string(role)
}

// pairGuardsMu guards pairGuards, the set of tests that armed a PairGuard.
var (
	pairGuardsMu sync.Mutex
	pairGuards   = make(map[*testing.T]bool)
)

// markPairGuard records whether t has an armed PairGuard.
func markPairGuard(t *testing.T, armed bool) {
	pairGuardsMu.Lock()
	defer pairGuardsMu.Unlock()
	if armed {
		pairGuards[t] = true
		return
	}
	delete(pairGuards, t)
}

// pairGuardArmed reports whether t armed a PairGuard.
func pairGuardArmed(t *testing.T) bool {
	pairGuardsMu.Lock()
	defer pairGuardsMu.Unlock()
	return pairGuards[t]
}

// requirePairGuard fails the test when it did not arm a PairGuard first.
// "defer PairGuard(t)()" evaluates PairGuard now and defers the returned
// function; the common mistake, "defer PairGuard(t)" without the last pair,
// defers PairGuard itself, so the guard arms only when the test returns and the
// other host waits out its bound. PairWrite and PairAwait check the arm so that
// omission fails at once.
func requirePairGuard(t *testing.T, what string) {
	t.Helper()
	if !pairGuardArmed(t) {
		t.Fatalf("syscontainer: %s needs the failure guard; write defer PairGuard(t)() at the start of the test", what)
	}
}

// PairGuard records this host's failure in the shared directory. The test
// defers the returned function, which runs as the test goroutine unwinds at the
// failure, before any t.Cleanup call, so the other host learns of the failure
// without waiting out its own bound. t.Failed still reports false while a panic
// unwinds, so the returned function recovers the panic, writes the record and
// panics again with the same value. A panic outside the test goroutine runs no
// deferred call here, and a process killed without unwinding runs none at all,
// so PairAwait's own bound has to cover those cases.
func PairGuard(t *testing.T) func() {
	t.Helper()
	role := PairRole()
	if role == "" {
		t.Fatalf("syscontainer: PairGuard runs inside a pair container as role A or B")
	}
	markPairGuard(t, true)
	return func() {
		markPairGuard(t, false)
		panicked := recover()
		if panicked == nil && !t.Failed() {
			return
		}
		message := fmt.Sprintf("host %s failed", role)
		if err := writePairFile(PairSharedDir(), pairFailureName(role), []byte(message)); err != nil {
			t.Logf("syscontainer: recording the failure of host %s: %v", role, err)
		}
		if panicked != nil {
			panic(panicked)
		}
	}
}

// PairWrite writes data to name inside the shared directory. The write goes to
// a temporary file first and is renamed into place, so the other host never
// reads a partial file.
func PairWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	requirePairGuard(t, "PairWrite")
	if err := writePairFile(PairSharedDir(), name, data); err != nil {
		t.Fatalf("syscontainer: writing %s to the shared directory: %v", name, err)
	}
}

// PairAwait waits up to bound for the other host to write name and returns its
// contents. The caller passes the bound its half of the scenario accounts for,
// because only it knows what the other host still has to do before writing the
// file. When the other host recorded a failure, the wait ends at once with that
// failure named.
func PairAwait(t *testing.T, name string, bound time.Duration) []byte {
	t.Helper()
	requirePairGuard(t, "PairAwait")
	dir := PairSharedDir()
	if dir == "" {
		t.Fatalf("syscontainer: PairAwait runs inside a pair container")
	}
	role := PairRole()
	if role == "" {
		t.Fatalf("syscontainer: PairAwait runs inside a pair container as role A or B")
	}
	failure := filepath.Join(dir, pairFailureName(peerRole(role)))

	var data []byte
	harness.Eventually(t, bound, "the other host to write "+name, func() (bool, string) {
		if text, err := os.ReadFile(failure); err == nil {
			t.Fatalf("the other host failed before writing %s: %s", name, text)
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			data = content
			return true, ""
		}
		if !os.IsNotExist(err) {
			return false, err.Error()
		}
		return false, name + " has not appeared"
	})
	return data
}

// writePairFile writes data to name under dir atomically. A name that is not a
// plain file name is refused so the exchange cannot write outside the directory.
func writePairFile(dir, name string, data []byte) error {
	if dir == "" {
		return fmt.Errorf("the shared directory is not set")
	}
	if !filepath.IsLocal(name) || name == "." || name == ".." {
		return fmt.Errorf("%q is not a plain file name", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".pair-")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("setting the mode of %s: %w", name, err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("renaming %s into place: %w", name, err)
	}
	return nil
}
