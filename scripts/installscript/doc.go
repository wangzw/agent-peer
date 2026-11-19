// Package installscript holds the tests that drive scripts/install.sh with
// fake command implementations. The fakes replace claude, stat, id, uname,
// xattr and install through PATH, so the installation paths can be exercised
// without root, a service manager or a real Claude Code installation.
package installscript
