//go:build !darwin

package helper

import (
	"fmt"
	"syscall"
)

// switchAccount replaces the process's supplementary groups, effective gid and
// effective uid, in that order. It is the Linux and BSD path; Darwin uses the
// initgroups system call instead, because its setgroups is capped at NGROUPS.
func switchAccount(uid, gid uint32, groups []uint32) error {
	intGroups := make([]int, len(groups))
	for i, group := range groups {
		intGroups[i] = int(group)
	}
	if err := syscall.Setgroups(intGroups); err != nil {
		return fmt.Errorf("setting groups %v: %w", groups, err)
	}
	if err := syscall.Setegid(int(gid)); err != nil {
		return fmt.Errorf("setting effective gid %d: %w", gid, err)
	}
	if err := syscall.Seteuid(int(uid)); err != nil {
		return fmt.Errorf("setting effective uid %d: %w", uid, err)
	}
	return nil
}
