package fakesession_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wangzw/peers/internal/inbox"
	"github.com/wangzw/peers/internal/testkit/fakesession"
)

// TestLookupEqualModTimesPrefersLargerPID covers Lookup's tie-break through
// fakesession fixtures: two records point at the same inbox socket and share a
// modification time, so the larger process id must win. The fake must allow
// equal modification times for this branch to be reachable.
func TestLookupEqualModTimesPrefersLargerPID(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "shared.sock")
	mod := time.Unix(1700000000, 0)

	lower := fakesession.New(t, fakesession.Options{
		ConfigDir:  dir,
		PID:        5411,
		Name:       "lower",
		NoBind:     true,
		SocketPath: socket,
	})
	higher := fakesession.New(t, fakesession.Options{
		ConfigDir:  dir,
		PID:        5412,
		Name:       "higher",
		NoBind:     true,
		SocketPath: socket,
	})
	lower.SetRecordModTime(mod)
	higher.SetRecordModTime(mod)

	for _, fake := range []*fakesession.Fake{lower, higher} {
		info, err := os.Stat(fake.RecordPath())
		if err != nil {
			t.Fatalf("stat record %s: %v", fake.RecordPath(), err)
		}
		if !info.ModTime().Equal(mod) {
			t.Fatalf("record %s modification time = %v, want %v",
				fake.RecordPath(), info.ModTime(), mod)
		}
	}

	s, err := inbox.Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if s.PID != 5412 {
		t.Fatalf("Lookup chose PID %d, want the larger 5412", s.PID)
	}
	if s.Name != "higher" {
		t.Fatalf("Lookup chose name %q, want higher", s.Name)
	}
}
