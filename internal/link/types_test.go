package link

import (
	"errors"
	"fmt"
	"testing"

	"github.com/wangzw/peers/internal/pairing"
)

// The peer table must satisfy the narrow interface link depends on.
var _ Peers = (*pairing.Pairing)(nil)

func TestErrQueueFullIsNotEstablished(t *testing.T) {
	if !errors.Is(ErrQueueFull, ErrNotEstablished) {
		t.Fatalf("errors.Is(ErrQueueFull, ErrNotEstablished) = false, want true")
	}
	if errors.Is(ErrNotEstablished, ErrQueueFull) {
		t.Fatalf("errors.Is(ErrNotEstablished, ErrQueueFull) = true, want false")
	}
}

func TestUnsupportedErrorMessage(t *testing.T) {
	e := &UnsupportedError{Kind: 301}
	if got, want := e.Error(), "link: peer does not support request kind 301"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	wrapped := fmt.Errorf("x: %w", e)
	var got *UnsupportedError
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As did not recover *UnsupportedError from %v", wrapped)
	}
	if got.Kind != 301 {
		t.Fatalf("recovered kind = %d, want 301", got.Kind)
	}
}
