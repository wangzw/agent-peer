//go:build darwin

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// envLockHolders finds the processes that have lockPath open, excluding self.
// macOS has no /proc, so lsof is asked for the path; it exits 1 when no process
// matches, which is not an error here. The caller opens the lock file to test
// the lock, so its own descriptor would otherwise make the checker look like a
// holder.
func envLockHolders(lockPath string, self int) []int {
	out, err := exec.Command("lsof", "-w", "-Fpn", lockPath).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil
		}
	}
	return envLsofPIDs(string(out), self)
}

// envProcessExe returns the executable path of pid, or "" when it cannot be
// read, for example because the process has exited. lsof reports the program
// text file as an n field.
func envProcessExe(pid int) string {
	out, err := exec.Command("lsof", "-w", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}
