package helper

import (
	"context"
	"time"

	"github.com/wangzw/agent-peer/internal/inbox"
)

// SessionOps is the set of inbox operations the account helper runs as its account.
type SessionOps interface {
	Lookup(configDir, socketPath string) (inbox.Session, error)
	Reread(s inbox.Session) (inbox.Session, error)
	Watch(configDir string, onChange func(pid int)) (stop func(), err error)
	Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error
}

// InboxOps implements SessionOps with package inbox.
type InboxOps struct{}

func (InboxOps) Lookup(configDir, socketPath string) (inbox.Session, error) {
	return inbox.Lookup(configDir, socketPath)
}

func (InboxOps) Reread(s inbox.Session) (inbox.Session, error) {
	return inbox.Reread(s)
}

func (InboxOps) Watch(configDir string, onChange func(pid int)) (stop func(), err error) {
	return inbox.Watch(configDir, onChange)
}

func (InboxOps) Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error {
	return inbox.Deliver(ctx, s, sig, text, msgID, timeout)
}

var _ SessionOps = InboxOps{}
