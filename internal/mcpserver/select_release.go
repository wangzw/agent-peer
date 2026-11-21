//go:build !peers_testenv

package mcpserver

import "context"

// findNamedSocket is a no-op in a release build: no environment variable can
// name a socket, so selection depends only on the calling account. The hook
// that reads one exists only in builds with the peers_testenv tag.
func (*selector) findNamedSocket(context.Context) (target, bool, error) {
	return target{}, false, nil
}
