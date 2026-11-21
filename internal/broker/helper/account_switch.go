package helper

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// The account-helper receives the identity it must switch to in its own
// variables, separate from the access check's. The two hidden subcommands have
// different jobs, so a child must never read an identity meant for the other.
// The Broker constructs both environments itself.
const (
	accountHelperUIDEnv = "PEERS_ACCOUNT_HELPER_UID"
	accountHelperGIDEnv = "PEERS_ACCOUNT_HELPER_GID"
)

// helperIdentityEnv returns the environment entries that carry acct's identity
// to an account-helper child that switches itself.
func helperIdentityEnv(acct Account) []string {
	return []string{
		accountHelperUIDEnv + "=" + strconv.FormatUint(uint64(acct.UID), 10),
		accountHelperGIDEnv + "=" + strconv.FormatUint(uint64(acct.GID), 10),
	}
}

// helperIdentityOps is the identity switch the account-helper performs when it
// starts as root. The fields are seams: Darwin supplies the syscall wrappers,
// and the unit tests supply recorders.
type helperIdentityOps struct {
	geteuid    func() int
	setgid     func(gid int) error
	initgroups func(gids []uint32, gmuid uint32) error
	setuid     func(uid int) error
	getuid     func() int
	getgid     func() int
	getegid    func() int
	getgroups  func() ([]int, error)
}

// enterHelperAccountFromEnv switches the process to the account named in the
// environment. Without the identity variables the process is already the
// account: that is how Linux starts the child (the credential is set before
// exec) and how unit tests run it in process.
//
// A process that is not root must refuse to switch: only the Broker can hand it
// an account identity, so an ordinary account cannot borrow another one. The
// switch sets the real, effective and saved gid first, then the supplementary
// groups, then the real, effective and saved uid. Only the primary group goes
// into the static initgroups list; Darwin's list is capped at NGROUPS, and the
// account uid as gmuid makes the kernel resolve the remaining groups through
// memberd. That is why an account in more than NGROUPS groups still switches.
//
// After the switch the process must prove it dropped root: the four ids must
// match, setuid(0) must fail, and, unless the account's primary group is 0, the
// supplementary groups must not contain 0. The identity variables are cleared
// once they have been consumed.
func enterHelperAccountFromEnv(ops helperIdentityOps) error {
	uid, gid, present, err := lookupIdentityEnv(accountHelperUIDEnv, accountHelperGIDEnv)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if euid := ops.geteuid(); euid != 0 {
		return fmt.Errorf("must start as root to switch to uid %d, running as uid %d", uid, euid)
	}
	// A helper that switches to uid 0 has not dropped root at all. The Broker
	// never asks for it, so this is a defect in the caller or the environment.
	if uid == 0 {
		return errors.New("refusing to switch to uid 0: the account-helper must not keep root")
	}

	if err := ops.setgid(int(gid)); err != nil {
		return fmt.Errorf("setting gid %d: %w", gid, err)
	}
	if err := ops.initgroups([]uint32{gid}, uid); err != nil {
		return fmt.Errorf("setting groups for uid %d: %w", uid, err)
	}
	if err := ops.setuid(int(uid)); err != nil {
		return fmt.Errorf("setting uid %d: %w", uid, err)
	}

	// A switch that did not take must not keep serving while privileged. Check
	// every half of the identity: a process that still holds root, or that
	// ended up in the wrong group, is stopped here.
	if got := ops.getuid(); got != int(uid) {
		return fmt.Errorf("after the switch the real uid is %d, want %d", got, uid)
	}
	if got := ops.geteuid(); got != int(uid) {
		return fmt.Errorf("after the switch the effective uid is %d, want %d", got, uid)
	}
	if got := ops.getgid(); got != int(gid) {
		return fmt.Errorf("after the switch the real gid is %d, want %d", got, gid)
	}
	if got := ops.getegid(); got != int(gid) {
		return fmt.Errorf("after the switch the effective gid is %d, want %d", got, gid)
	}

	// The four ids can all read correctly while the process still holds root in
	// a saved uid or a capability. Trying setuid(0) is how OpenSSH's
	// permanently_set_uid catches that: if it succeeds, the drop did not take.
	if err := ops.setuid(0); err == nil {
		return errors.New("after the switch setuid(0) succeeded: the account-helper still holds root")
	}

	// The account's own primary group may be gid 0; any other group table that
	// still contains 0 keeps access root has and the account must not.
	if gid != 0 {
		groups, err := ops.getgroups()
		if err != nil {
			return fmt.Errorf("reading the supplementary groups after the switch: %w", err)
		}
		for _, g := range groups {
			if g == 0 {
				return errors.New("after the switch the supplementary groups contain gid 0")
			}
		}
	}

	// The identity has been consumed; do not leave it in the environment for
	// anything this process later starts.
	if err := os.Unsetenv(accountHelperUIDEnv); err != nil {
		return fmt.Errorf("clearing %s: %w", accountHelperUIDEnv, err)
	}
	if err := os.Unsetenv(accountHelperGIDEnv); err != nil {
		return fmt.Errorf("clearing %s: %w", accountHelperGIDEnv, err)
	}
	return nil
}
