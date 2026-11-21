//go:build peers_testenv

package main

import (
	"os"
	"strconv"
)

// parseProtocolOverrides reads the protocol versions an end-to-end test asks
// for. An absent value, a malformed value or one outside uint32 yields 0,
// which the Broker and the MCP server replace with the version of the
// generated packages.
func parseProtocolOverrides(getenv func(string) string) (local, link uint32) {
	return parseProtocolVersion(getenv("PEERS_TEST_LOCAL_PROTOCOL_VERSION")),
		parseProtocolVersion(getenv("PEERS_TEST_LINK_PROTOCOL_VERSION"))
}

// parseProtocolVersion parses one override value. ParseUint rejects a sign, so
// a negative number is an error and becomes 0 like any other malformed value.
func parseProtocolVersion(v string) uint32 {
	if v == "" {
		return 0
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// init replaces the test seam of the command line. The replacement reads the
// environment on every call rather than caching it at init time, so a test can
// change the value before it launches a process.
func init() {
	protocolOverrides = func() (local, link uint32) {
		return parseProtocolOverrides(os.Getenv)
	}
}
