// Package fakesession provides a fake Claude Code session for tests.
//
// A Fake writes a session registry record under a temporary configuration
// directory, binds an inbox Unix socket, accepts the newline-delimited JSON
// frame a sender writes, and records what it received. It never starts Claude
// Code and never touches a real session's files or sockets.
//
// The package depends only on the standard library. It is intended to be
// imported from _test.go files, including end-to-end tests that need a
// receiving session without running Claude Code.
//
// The fake mirrors the receiver's frame handling: a frame addressed to another
// session is dropped silently, a line that is not a JSON object is not a frame,
// and a from-name that receiver cleaning would change leaves the attribution
// envelope unrecognized.
package fakesession
