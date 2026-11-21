//go:build darwin

package helper

import (
	"os"
	"os/exec"
	"syscall"
)

// accountChildIdentity hands the account identity to the child through the
// environment instead of the credential. Darwin's setgroups and the static
// initgroups list are capped at NGROUPS (16), so an account in more groups
// cannot be expressed in syscall.Credential.Groups: the credential change would
// fail with EINVAL before the helper started. The child starts as root and
// switches itself in enterHelperAccount.
func accountChildIdentity(cmd *exec.Cmd, acct Account) {
	cmd.Env = append(cmd.Env, helperIdentityEnv(acct)...)
}

// enterHelperAccount switches a root-started account-helper to its account
// before any protocol byte is served. It reuses the Darwin initgroups system
// call the access check already depends on, so the kernel resolves the
// account's groups through memberd instead of a static table.
func enterHelperAccount() error {
	return enterHelperAccountFromEnv(helperIdentityOps{
		geteuid:    os.Geteuid,
		setgid:     syscall.Setgid,
		initgroups: callInitgroups,
		setuid:     syscall.Setuid,
		getuid:     os.Getuid,
		getgid:     os.Getgid,
		getegid:    os.Getegid,
		getgroups:  syscall.Getgroups,
	})
}

// setParentDeathSignal is a no-op where the kernel has no parent death signal.
func setParentDeathSignal(*syscall.SysProcAttr) {}
