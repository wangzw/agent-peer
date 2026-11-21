package directory

import "github.com/wangzw/peers/internal/link"

// NewLinkTransport adapts the link package to the directory's Links
// interface. The directory assembly calls it with the process-wide
// *link.Link; the returned Links forwards every call to that link.
func NewLinkTransport(l *link.Link) Links { return newLinkTransport(l) }
