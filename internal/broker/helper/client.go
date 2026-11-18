package helper

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/helperpb"
	"github.com/wangzw/agent-peer/internal/inbox"
)

var (
	// ErrHelperGone reports that the connection to the account helper is lost.
	ErrHelperGone = errors.New("helper: account helper connection lost")
	// ErrProtocol reports a violation of the helper pipe protocol.
	ErrProtocol = errors.New("helper: protocol violation")
)

// Client is the Broker side of one account helper connection.
type Client struct{}

// NewClient starts the read loop over r (the child's stdout) and writes requests to w (its stdin).
func NewClient(r io.Reader, w io.WriteCloser) *Client {
	return &Client{}
}

// WaitReady returns the Ready message; ErrProtocol if the first message is not Ready.
func (c *Client) WaitReady(ctx context.Context) (*helperpb.Ready, error) {
	return nil, ErrHelperGone
}

func (c *Client) Lookup(ctx context.Context, configDir, socketPath string) (inbox.Session, error) {
	return inbox.Session{}, ErrHelperGone
}

func (c *Client) Reread(ctx context.Context, s inbox.Session) (inbox.Session, error) {
	return inbox.Session{}, ErrHelperGone
}

func (c *Client) Watch(ctx context.Context, configDir string, onChange func(pid int)) (stop func(), err error) {
	return nil, ErrHelperGone
}

func (c *Client) Deliver(ctx context.Context, s inbox.Session, sig, text, msgID string, timeout time.Duration) error {
	return ErrHelperGone
}

// Done is closed once the read loop ends.
func (c *Client) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// Close closes w; it is idempotent.
func (c *Client) Close() error {
	return nil
}
