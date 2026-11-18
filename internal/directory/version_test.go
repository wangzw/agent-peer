package directory

import (
	"math"
	"testing"
	"time"
)

func TestNextVersionUsesClock(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	want := uint64(now.UnixMilli())
	if got := nextVersion(now, 0); got != want {
		t.Fatalf("nextVersion(%v, 0) = %d, want %d", now, got, want)
	}
}

func TestNextVersionUsesClockWhenHigher(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	highest := uint64(now.UnixMilli()) - 1000
	want := uint64(now.UnixMilli())
	if got := nextVersion(now, highest); got != want {
		t.Fatalf("nextVersion with the clock ahead of a non-zero highest = %d, want %d", got, want)
	}
}

func TestNextVersionClockBackwards(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	highest := uint64(now.UnixMilli()) + uint64(time.Hour/time.Millisecond)
	want := highest + 1
	if got := nextVersion(now, highest); got != want {
		t.Fatalf("nextVersion with highest one hour ahead = %d, want %d", got, want)
	}
}

func TestNextVersionSaturates(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if got := nextVersion(now, math.MaxUint64); got != math.MaxUint64 {
		t.Fatalf("nextVersion(math.MaxUint64) = %d, want math.MaxUint64", got)
	}
}

func TestNextVersionNegativeClock(t *testing.T) {
	before := time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC) // negative Unix milliseconds
	if got := nextVersion(before, 0); got != 1 {
		t.Fatalf("nextVersion(%v, 0) = %d, want 1", before, got)
	}
}

func TestExpiryBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

	if !isExpired(now, now) {
		t.Errorf("isExpired(expires == now) = false, want true")
	}
	if isExpired(now.Add(time.Nanosecond), now) {
		t.Errorf("isExpired(expires one nanosecond in the future) = true, want false")
	}

	if expiryTooFar(now.Add(recordTTL+skewTolerance), now) {
		t.Errorf("expiry at now+recordTTL+skewTolerance reported too far")
	}
	if !expiryTooFar(now.Add(recordTTL+skewTolerance+time.Nanosecond), now) {
		t.Errorf("expiry one nanosecond past the tolerance not reported too far")
	}
}

func TestRenewalDueBoundary(t *testing.T) {
	issued := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	expires := issued.Add(recordTTL)

	if got := issuedAt(expires); !got.Equal(issued) {
		t.Errorf("issuedAt(expires) = %v, want %v", got, issued)
	}
	if renewalDue(expires, issued.Add(24*time.Hour-time.Minute)) {
		t.Errorf("renewal due at 23h59m after issuance, want false")
	}
	if !renewalDue(expires, issued.Add(renewAfter)) {
		t.Errorf("renewal not due at 24h after issuance")
	}
}
