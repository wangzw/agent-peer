package helper

import (
	"context"
	"io"
	"log/slog"
	"time"
)

type Access uint8

const (
	AccessRead Access = iota + 1
	AccessWrite
)

// RunAccessCheck is the body of `agent-peer internal access-check`: it reads one
// AccessCheckRequest from stdin, checks it as the current process, writes one
// AccessCheckResponse to stdout and returns.
func RunAccessCheck(stdin io.Reader, stdout io.Writer) error {
	return errNotImplemented
}

type AccessChecker struct {
	Exe     string                                                    // required unless Start is set
	Lookup  func(uid uint32) (Account, error)                         // default LookupAccount
	Timeout time.Duration                                             // default 5s
	Start   func(ctx context.Context, acct Account) (*Process, error) // default: accountCommand(Exe, []string{"internal", "access-check"}, acct) with pipes
	Logger  *slog.Logger
}

// Check reports whether the account uid may access path. It spawns one process per call.
func (c *AccessChecker) Check(ctx context.Context, uid uint32, path string, need Access) (bool, error) {
	return false, errNotImplemented
}
