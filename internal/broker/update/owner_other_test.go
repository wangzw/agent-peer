//go:build !unix

package update

import (
	"os"
	"testing"
)

// assertSameOwner is a no-op where file ownership is not a Unix concept.
func assertSameOwner(t *testing.T, _ string, _ os.FileInfo) {
	t.Helper()
}
