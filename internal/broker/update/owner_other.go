//go:build !unix

package update

import "os"

// chownFile is a no-op where file ownership is not a Unix concept.
func chownFile(*os.File, os.FileInfo) error {
	return nil
}
