//go:build agentpeer_testenv

package broker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOffsetClockReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clock")
	if err := os.WriteFile(path, []byte("48h"), 0o600); err != nil {
		t.Fatalf("writing the clock file: %v", err)
	}

	clock := offsetClock(path)
	want := time.Now().Add(48 * time.Hour)
	if got := clock(); got.Sub(want).Abs() > time.Second {
		t.Fatalf("clock = %v, want within 1s of %v", got, want)
	}

	// Shrinking the content must be observed on the next call.
	if err := os.WriteFile(path, []byte("0s"), 0o600); err != nil {
		t.Fatalf("rewriting the clock file: %v", err)
	}
	now := time.Now()
	if got := clock(); got.Sub(now).Abs() > time.Second {
		t.Fatalf("clock after rewrite = %v, want within 1s of %v", got, now)
	}
}

func TestOffsetClockMissingFile(t *testing.T) {
	clock := offsetClock(filepath.Join(t.TempDir(), "absent"))

	now := time.Now()
	if got := clock(); got.Sub(now).Abs() > time.Second {
		t.Fatalf("clock with a missing file = %v, want within 1s of %v", got, now)
	}
}

func TestOffsetClockInvalidContentFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clock")
	if err := os.WriteFile(path, []byte("not a duration"), 0o600); err != nil {
		t.Fatalf("writing the clock file: %v", err)
	}

	clock := offsetClock(path)
	now := time.Now()
	if got := clock(); got.Sub(now).Abs() > time.Second {
		t.Fatalf("clock with invalid content = %v, want within 1s of %v", got, now)
	}

	// A valid rewrite must be picked up after the invalid content.
	if err := os.WriteFile(path, []byte("24h"), 0o600); err != nil {
		t.Fatalf("rewriting the clock file: %v", err)
	}
	want := time.Now().Add(24 * time.Hour)
	if got := clock(); got.Sub(want).Abs() > time.Second {
		t.Fatalf("clock after a valid rewrite = %v, want within 1s of %v", got, want)
	}
}
