//go:build darwin

package helper

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sysInitgroups is Darwin's initgroups system call, number 243 in
// bsd/kern/syscalls.master:
//
//	initgroups(u_int gidsetsize, gid_t *gidset, int gmuid) NO_SYSCALL_STUB
//
// It is a private interface. syscalls.master marks it NO_SYSCALL_STUB because
// no libc wrapper takes the gmuid argument; x/sys/unix's Syscall issues the
// trap directly (SVC 0x80 on darwin/arm64), so this call enters the kernel
// without going through libc. The number and argument order are therefore
// encoded here. It is not the libc initgroups(name, basegid) function, which
// expands the whole group list itself and is bounded by NGROUPS. The macOS
// end-to-end test guards this call: a change to the number or the layout fails
// that test before a release.
const sysInitgroups = 243

// darwinNGroups is NGROUPS in Darwin's sys/param.h. The static gidset passed
// to initgroups may contain at most this many entries; gmuid lets the kernel
// resolve the account's remaining groups through memberd.
const darwinNGroups = 16

// The syscall seams are variables so the Darwin unit test can check the call
// sequence without being root.
var (
	switchSetegid    = syscall.Setegid
	switchSeteuid    = syscall.Seteuid
	switchInitgroups = callInitgroups
)

// switchAccount switches to uid without passing the account's full group list.
// Darwin's setgroups and the static initgroups list are capped at NGROUPS, so
// an account in more groups would fail with EINVAL. Passing only the primary
// group and the account's uid as gmuid makes the kernel resolve the remaining
// groups dynamically, which also covers ACL entries that name one of them.
//
// The order matters: setegid and initgroups both need the process to be root,
// and initgroups requires the process credential to still be privileged, so
// the effective uid is dropped last.
func switchAccount(uid, gid uint32, _ []uint32) error {
	if err := switchSetegid(int(gid)); err != nil {
		return fmt.Errorf("setting effective gid %d: %w", gid, err)
	}
	if err := switchInitgroups([]uint32{gid}, uid); err != nil {
		return fmt.Errorf("setting groups for uid %d: %w", uid, err)
	}
	if err := switchSeteuid(int(uid)); err != nil {
		return fmt.Errorf("setting effective uid %d: %w", uid, err)
	}
	return nil
}

// callInitgroups invokes the Darwin initgroups system call with a static group
// list of at most NGROUPS entries and the account uid as the group membership
// uid. A nonzero errno is returned as the error, so the caller fails the
// access check closed.
func callInitgroups(gids []uint32, gmuid uint32) error {
	if len(gids) == 0 {
		return fmt.Errorf("initgroups needs at least one group")
	}
	if len(gids) > darwinNGroups {
		return fmt.Errorf("initgroups list has %d groups, Darwin allows %d", len(gids), darwinNGroups)
	}
	_, _, errno := unix.Syscall(sysInitgroups, uintptr(len(gids)), uintptr(unsafe.Pointer(&gids[0])), uintptr(gmuid))
	runtime.KeepAlive(gids)
	if errno != 0 {
		return errno
	}
	return nil
}
