package directory

import (
	"testing"

	"github.com/wangzw/agent-peer/internal/link"
)

// The link package must keep the method set the adapter relies on. A change
// to any of these signatures fails to compile here.
var _ linkAPI = (*link.Link)(nil)

func TestNewLinkTransportNotNil(t *testing.T) {
	links := NewLinkTransport(nil)
	if links == nil {
		t.Fatal("NewLinkTransport(nil) returned nil, want a non-nil Links")
	}
}
