// Package syscontainer runs the system-level end-to-end tests inside
// containers. This file carries no e2e build tag so that the role parsing and
// the shared-directory exchange compile and run on the host without one.
package syscontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
)

const (
	// PairRoleEnv carries the role into a container that is part of a pair.
	// RunPairTest sets it to "A" for hostA and "B" for hostB.
	PairRoleEnv = "AGENT_PEER_E2E_PAIR_ROLE"

	// pairSharedEnv carries the shared directory path into a pair container.
	// The value is the container path, not the host path.
	pairSharedEnv = "AGENT_PEER_E2E_PAIR_SHARED"

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

// PairGuard records this host's failure in the shared directory. The test calls
// it first; a failing test then tells the other host before the process exits,
// and PairAwait turns that record into a failure on the other side. A process
// killed without unwinding leaves no record, which is why PairAwait also has
// its own bound.
func PairGuard(t *testing.T) {
	t.Helper()
	role := PairRole()
	if role == "" {
		t.Fatalf("syscontainer: PairGuard runs inside a pair container as role A or B")
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		message := fmt.Sprintf("host %s failed", role)
		if err := writePairFile(PairSharedDir(), pairFailureName(role), []byte(message)); err != nil {
			t.Logf("syscontainer: recording the failure of host %s: %v", role, err)
		}
	})
}

// PairWrite writes data to name inside the shared directory. The write goes to
// a temporary file first and is renamed into place, so the other host never
// reads a partial file.
func PairWrite(t *testing.T, name string, data []byte) {
	t.Helper()
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
