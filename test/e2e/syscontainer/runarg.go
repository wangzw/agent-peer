// Package syscontainer runs the system end-to-end tests inside
// containers. This file carries no e2e build tag so that the pure helper below
// and its host-side test compile and run without one.
package syscontainer

import (
	"regexp"
	"strings"
)

// runTestFlag turns a test name into the -test.run pattern that selects exactly
// that test, including a subtest. Each slash-separated segment is anchored and
// escaped, so a name containing regular-expression characters cannot match a
// different test.
func runTestFlag(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	return strings.Join(parts, "/")
}
