// Package linktest provides fakes for testing the link package and the
// packages built on it.
//
// A test can start several identities in one process: FakePeers supplies a
// peer table with its own Ed25519 identity, FakeSettings supplies link's
// configuration view, FakeClock makes timers fire on demand, and Proxy sits
// between two endpoints so a test can pause, capture and cut the bytes on a
// connection. None of the fakes import internal/link, so the link package's own
// tests can use them without an import cycle.
//
// The package is intended to be imported from _test.go files and end-to-end
// tests only. It depends on the pairing and config packages to mirror their
// interfaces, but never on link.
package linktest
