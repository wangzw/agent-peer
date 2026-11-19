// Package pathcmp compares filesystem paths as the kernel reports them.
package pathcmp

import (
	"fmt"
	"path/filepath"
)

// Resolve expands every symbolic link in path so that two spellings of the
// same location compare equal. macOS reports a directory created under /var
// back as its target under /private/var once the kernel resolves the link.
func Resolve(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return resolved, nil
}

// SamePath reports whether a and b name the same location once symbolic links
// in both are resolved.
func SamePath(a, b string) (bool, error) {
	resolvedA, err := Resolve(a)
	if err != nil {
		return false, err
	}
	resolvedB, err := Resolve(b)
	if err != nil {
		return false, err
	}
	return resolvedA == resolvedB, nil
}
