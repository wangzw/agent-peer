package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/wangzw/peers/internal/mcpserver/termcmd"
	"github.com/wangzw/peers/internal/testkit/fakelocal"
)

// startApplyUpdateBroker serves one ApplyUpdate answer at a fresh socket and
// selects it through findBroker. The answer configures the fake's handlers.
func startApplyUpdateBroker(t *testing.T, answer func(*fakelocal.Handlers)) (string, *fakelocal.Server) {
	t.Helper()
	path := fakelocal.SocketPath(t)
	h := fakelocal.AdminHandlers()
	answer(&h)
	srv := fakelocal.Start(t, path, h)

	previous := findBroker
	findBroker = fakelocal.Selector(path)
	t.Cleanup(func() { findBroker = previous })
	return path, srv
}

func withUserSocketPath(t *testing.T, path string) {
	t.Helper()
	previous := updateUserSocketPath
	updateUserSocketPath = func() string { return path }
	t.Cleanup(func() { updateUserSocketPath = previous })
}

// fixedEUID returns an euid function for the tests, replacing the account the
// test process runs as.
func fixedEUID(euid int) func() int {
	return func() int { return euid }
}

// An ordinary account with no user-level Broker has the system-level Broker
// selected, and the update is applied through it.
func TestApplyUpdateThroughBrokerUsesSystemBrokerWithoutUserBroker(t *testing.T) {
	newUpdateTestEnv(t) // euid 1000
	path, srv := startApplyUpdateBroker(t, func(h *fakelocal.Handlers) {
		h.ApplyUpdate = fakelocal.ApplyUpdateAnswer("0.1.1", "0.1.2", true)
	})
	withUserSocketPath(t, path+".user")

	got, err := applyUpdateThroughBroker(t.Context(), fixedEUID(1000))
	if err != nil {
		t.Fatalf("applyUpdateThroughBroker: %v", err)
	}
	if got.FromVersion != "0.1.1" || got.ToVersion != "0.1.2" || !got.Restarting {
		t.Fatalf("result = %+v, want 0.1.1 -> 0.1.2 restarting", got)
	}
	if n := wireMethodCalls(srv, "ApplyUpdate"); n != 1 {
		t.Fatalf("ApplyUpdate calls = %d, want 1 on the system-level Broker", n)
	}
	if got.UserLevel {
		t.Error("UserLevel = true for the system-level Broker, want false")
	}
}

// The user-level Broker that applies the update is this account's own, so the
// command marks the result user-level and a timeout names the user's log.
func TestApplyUpdateThroughBrokerMarksTheUserLevel(t *testing.T) {
	newUpdateTestEnv(t) // euid 1000
	path, _ := startApplyUpdateBroker(t, func(h *fakelocal.Handlers) {
		h.ApplyUpdate = fakelocal.ApplyUpdateAnswer("0.1.1", "0.1.2", true)
	})
	withUserSocketPath(t, path)

	got, err := applyUpdateThroughBroker(t.Context(), fixedEUID(1000))
	if err != nil {
		t.Fatalf("applyUpdateThroughBroker: %v", err)
	}
	if !got.UserLevel {
		t.Error("UserLevel = false for this account's user-level Broker, want true")
	}
}

// An ordinary account with its own user-level Broker selected falls back to a
// local replacement when that Broker declines the update.
func TestApplyUpdateThroughBrokerFallsBackForUserBroker(t *testing.T) {
	newUpdateTestEnv(t) // euid 1000
	path, _ := startApplyUpdateBroker(t, func(h *fakelocal.Handlers) {
		h.ApplyUpdate = fakelocal.ApplyUpdateFailure(codes.FailedPrecondition, "this Broker cannot replace and restart its binary")
	})
	withUserSocketPath(t, path)

	_, err := applyUpdateThroughBroker(t.Context(), fixedEUID(1000))
	if !errors.Is(err, errUserLevelUpdateUnsupported) {
		t.Fatalf("applyUpdateThroughBroker error = %v, want the user-level fallback", err)
	}
}

// Root has one socket for both levels, and an update there belongs to the
// system-level Broker: a refusal is an error, never a local replacement.
func TestApplyUpdateThroughBrokerRootUsesSystemBroker(t *testing.T) {
	newUpdateTestEnv(t)
	path, _ := startApplyUpdateBroker(t, func(h *fakelocal.Handlers) {
		h.ApplyUpdate = fakelocal.ApplyUpdateFailure(codes.FailedPrecondition, "the Broker is not managed by a service manager")
	})
	withUserSocketPath(t, path)

	_, err := applyUpdateThroughBroker(t.Context(), fixedEUID(0))
	if err == nil {
		t.Fatal("applyUpdateThroughBroker returned no error, want the refusal")
	}
	if errors.Is(err, errUserLevelUpdateUnsupported) {
		t.Fatalf("applyUpdateThroughBroker error = %v, want the refusal without a local fallback", err)
	}
	if !strings.Contains(err.Error(), "FailedPrecondition") {
		t.Fatalf("error = %q, want the Broker's failed_precondition", err)
	}
}

// A failure other than failed_precondition is reported, not turned into a
// local replacement.
func TestApplyUpdateThroughBrokerReportsOtherErrors(t *testing.T) {
	newUpdateTestEnv(t)
	path, _ := startApplyUpdateBroker(t, func(h *fakelocal.Handlers) {
		h.ApplyUpdate = fakelocal.ApplyUpdateFailure(codes.PermissionDenied, "the account cannot write the Broker configuration")
	})
	withUserSocketPath(t, path)

	_, err := applyUpdateThroughBroker(t.Context(), fixedEUID(1000))
	if err == nil {
		t.Fatal("applyUpdateThroughBroker returned no error, want the permission failure")
	}
	if errors.Is(err, errUserLevelUpdateUnsupported) {
		t.Fatalf("applyUpdateThroughBroker error = %v, want no local fallback for a permission failure", err)
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("error = %q, want the Broker's permission failure", err)
	}
}

// Without a running Broker there is nothing to ask, so the command replaces
// the binary locally.
func TestApplyUpdateThroughBrokerWithoutRunningBroker(t *testing.T) {
	newUpdateTestEnv(t)
	previous := findBroker
	findBroker = func(context.Context) (string, termcmd.Dialer, error) {
		return "", nil, errors.New("no Broker is running")
	}
	t.Cleanup(func() { findBroker = previous })

	_, err := applyUpdateThroughBroker(t.Context(), fixedEUID(1000))
	if !errors.Is(err, errNoRunningBroker) {
		t.Fatalf("applyUpdateThroughBroker error = %v, want the no-running-Broker fallback", err)
	}
}
