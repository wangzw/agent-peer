//go:build !linux && !darwin

package helper

import (
	"os/exec"
	"syscall"
)

// accountChildIdentity hands the account identity to the kernel before exec.
// The BSDs this file covers share Linux's behavior of passing the full group
// table in the credential.
func accountChildIdentity(cmd *exec.Cmd, acct Account) {
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    acct.UID,
		Gid:    acct.GID,
		Groups: acct.Groups,
	}
}

// enterHelperAccount is a no-op where the child is already the account when it
// starts.
func enterHelperAccount() error { return nil }

// setParentDeathSignal is a no-op where the kernel has no parent death signal.
func setParentDeathSignal(*syscall.SysProcAttr) {}
