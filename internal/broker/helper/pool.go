package helper

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// Process is one started helper child.
type Process struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Wait   func() error // reaps the child
	Kill   func() error
}

type PoolOptions struct {
	Exe          string                                                    // required unless Start is set
	Lookup       func(uid uint32) (Account, error)                         // default LookupAccount
	Start        func(ctx context.Context, acct Account) (*Process, error) // default: accountCommand(Exe, []string{"internal", "account-helper"}, acct) with pipes
	ReadyTimeout time.Duration                                             // default 5s
	RespawnMin   time.Duration                                             // default 1s
	RespawnMax   time.Duration                                             // default 30s
	After        func(d time.Duration) <-chan time.Time                    // default time.After
	Logger       *slog.Logger                                              // default slog.Default()
}

// Pool supervises one account helper process per account.
type Pool struct{}

func NewPool(opts PoolOptions) *Pool {
	return &Pool{}
}

func (p *Pool) Acquire(ctx context.Context, uid uint32) (*Backend, func(), error) {
	return nil, nil, errNotImplemented
}

func (p *Pool) Close() error {
	return nil
}

// Backend is one account's view of the pool; methods mirror Client and survive helper restarts.
type Backend struct{}

func (b *Backend) Lookup(ctx context.Context, configDir, socketPath string) (inbox.Session, error) {
	return inbox.Session{}, errNotImplemented
}

func (b *Backend) Reread(ctx context.Context, s inbox.Session) (inbox.Session, error) {
	return inbox.Session{}, errNotImplemented
}

func (b *Backend) Watch(ctx context.Context, configDir string, onChange func(pid int)) (stop func(), err error) {
	return nil, errNotImplemented
}

func (b *Backend) Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error {
	return errNotImplemented
}
