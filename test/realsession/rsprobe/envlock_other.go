//go:build !linux && !darwin

package main

// envLockHolders finds the processes that have lockPath open, excluding self.
// The scan is unimplemented on this platform; the lock check then reports an
// unknown holder instead of guessing.
func envLockHolders(string, int) []int {
	return nil
}

// envProcessExe returns the executable path of pid, or "" when the platform
// offers no way to read it.
func envProcessExe(int) string {
	return ""
}
