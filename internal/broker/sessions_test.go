package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// Compile-time checks: the user-level backend satisfies SessionBackend, the
// user-level router satisfies SessionRouter, and every inbox function the
// backend forwards to has the signature sessions.go assumes. A change in the
// inbox API breaks this file at compile time instead of silently leaving a
// wrapper that calls the wrong thing.
var (
	_ SessionBackend = userBackend{}
	_ SessionRouter  = userRouter{}

	_ func(string, string) (inbox.Session, error)                                       = inbox.Lookup
	_ func(inbox.Session) (inbox.Session, error)                                        = inbox.Reread
	_ func(string, func(int)) (func(), error)                                           = inbox.Watch
	_ func(context.Context, inbox.Session, string, string, string, time.Duration) error = inbox.Deliver
)

// TestSessionBackendSignatures holds only the compile-time assertions above; it
// exists so the signature check has a named test in the suite.
func TestSessionBackendSignatures(t *testing.T) {}

// TestUserRouterServesAnyUID checks that the user-level router hands out a
// backend for any account without an extra check, and that the backend really
// forwards to inbox.
func TestUserRouterServesAnyUID(t *testing.T) {
	backend, release, err := UserRouter().Acquire(context.Background(), 4242)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if backend == nil {
		t.Fatal("Acquire returned a nil SessionBackend")
	}
	if release == nil {
		t.Fatal("Acquire returned a nil release function")
	}
	release()

	// An empty socket path never matches a record; the backend must report
	// inbox.ErrNoRecord rather than inventing an error of its own.
	_, err = backend.Lookup(context.Background(), t.TempDir(), "")
	if !errors.Is(err, inbox.ErrNoRecord) {
		t.Fatalf("Lookup with an empty socket path: got %v, want inbox.ErrNoRecord", err)
	}
}
