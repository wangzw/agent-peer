package helper

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// Compile-time checks that the inbox functions still have the signatures the
// helper package adapts.
var (
	_ func(string, string) (inbox.Session, error)                                       = inbox.Lookup
	_ func(inbox.Session) (inbox.Session, error)                                        = inbox.Reread
	_ func(string, func(int)) (func(), error)                                           = inbox.Watch
	_ func(context.Context, inbox.Session, string, string, string, time.Duration) error = inbox.Deliver
)

func TestInboxOpsLookupDelegates(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	record := `{"pid":4242,"name":"a","status":"idle","messagingSocketPath":"/x.sock"}`
	if err := os.WriteFile(filepath.Join(sessions, "4242.json"), []byte(record), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := InboxOps{}.Lookup(dir, "/x.sock")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if s.PID != 4242 {
		t.Errorf("PID = %d, want 4242", s.PID)
	}
	if s.Name != "a" {
		t.Errorf("Name = %q, want %q", s.Name, "a")
	}
}

func TestInboxOpsLookupMissingIsNoRecord(t *testing.T) {
	dir := t.TempDir()
	if _, err := (InboxOps{}).Lookup(dir, "/x.sock"); !errors.Is(err, inbox.ErrNoRecord) {
		t.Fatalf("Lookup error = %v, want inbox.ErrNoRecord", err)
	}
}

func TestStubSignatures(t *testing.T) {
	var (
		_ = (func(context.Context, io.Reader, io.Writer, SessionOps, Identity) error)(Serve)
		_ = (func(io.Reader, io.WriteCloser) *Client)(NewClient)
		_ = (func(PoolOptions) *Pool)(NewPool)
		_ = (func(io.Reader, io.Writer) error)(RunAccessCheck)
	)
}
