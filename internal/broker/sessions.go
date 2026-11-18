package broker

import (
	"context"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// SessionBackend performs inbox operations for one account. A user-level
// Broker calls the inbox package directly in the same process; a system-level
// Broker routes every call to a helper process running as the target account.
// Callers pass the ctx of the Local RPC being served; implementations that talk
// to a helper process must propagate it, and implementations that call inbox
// in-process may ignore it where inbox has no context to accept.
type SessionBackend interface {
	Lookup(ctx context.Context, configDir, socketPath string) (inbox.Session, error)
	Reread(ctx context.Context, s inbox.Session) (inbox.Session, error)
	Watch(ctx context.Context, configDir string, onChange func(pid int)) (stop func(), err error)
	Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error
}

// SessionRouter hands out the SessionBackend of an account. Acquire returns a
// release function that the caller must call when it stops using the backend;
// release is never nil, and calling it more than once is safe. A router may
// share one backend between concurrent holders, so release ends the holder's
// interest rather than tearing the backend down immediately.
type SessionRouter interface {
	Acquire(ctx context.Context, uid uint32) (b SessionBackend, release func(), err error)
}

// UserRouter returns the router of a user-level Broker, which serves exactly
// one account and therefore drives the inbox package in-process.
// It is used when Deps.Sessions is nil.
func UserRouter() SessionRouter {
	return userRouter{}
}

// userBackend is the SessionBackend of a user-level Broker. Every method calls
// the corresponding inbox function and returns its result unchanged.
type userBackend struct{}

func (userBackend) Lookup(_ context.Context, configDir, socketPath string) (inbox.Session, error) {
	return inbox.Lookup(configDir, socketPath)
}

func (userBackend) Reread(_ context.Context, s inbox.Session) (inbox.Session, error) {
	return inbox.Reread(s)
}

func (userBackend) Watch(_ context.Context, configDir string, onChange func(pid int)) (func(), error) {
	return inbox.Watch(configDir, onChange)
}

func (userBackend) Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error {
	return inbox.Deliver(ctx, s, sig, text, msgID, timeout)
}

// userRouter serves every uid with the in-process backend. The peer-credential
// handshake has already refused connections from other accounts, so no uid
// check belongs here.
type userRouter struct{}

func (userRouter) Acquire(context.Context, uint32) (SessionBackend, func(), error) {
	return userBackend{}, func() {}, nil
}
