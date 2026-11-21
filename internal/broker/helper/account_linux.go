package helper

import (
	"os/exec"
	"syscall"
)

// accountChildIdentity hands the account identity to the kernel before exec.
// Linux has no NGROUPS limit on setgroups, so the full group table travels in
// the credential.
func accountChildIdentity(cmd *exec.Cmd, acct Account) {
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    acct.UID,
		Gid:    acct.GID,
		Groups: acct.Groups,
	}
}

// enterHelperAccount is a no-op on Linux: the child is already the account when
// it starts.
func enterHelperAccount() error { return nil }

// setParentDeathSignal makes the kernel kill the child when the Broker dies, so
// a helper cannot outlive it while holding the account's inbox socket.
func setParentDeathSignal(a *syscall.SysProcAttr) {
	a.Pdeathsig = syscall.SIGKILL
}
