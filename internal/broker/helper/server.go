package helper

import (
	"context"
	"io"
)

// Identity is the credential the helper process runs with, reported in Ready.
type Identity struct {
	UID    uint32
	GID    uint32
	Groups []uint32
}

// CurrentIdentity returns the credential of the current process.
func CurrentIdentity() Identity {
	return Identity{}
}

// Serve runs the helper protocol until r reaches EOF, ctx ends, or writing to w fails.
// It returns nil after a clean EOF and an error otherwise.
func Serve(ctx context.Context, r io.Reader, w io.Writer, ops SessionOps, self Identity) error {
	return errNotImplemented
}

// RunAccountHelper is the body of `agent-peer internal account-helper`.
func RunAccountHelper(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return Serve(ctx, stdin, stdout, InboxOps{}, CurrentIdentity())
}
