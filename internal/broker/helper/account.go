package helper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/wangzw/agent-peer/internal/accountdb"
)

// Account is one entry of the account database.
type Account struct {
	Name   string
	UID    uint32
	GID    uint32   // primary group
	Groups []uint32 // supplementary groups, including GID, as the account database lists them
	Home   string
}

// ErrNoAccount reports that the account database has no entry for the uid.
var ErrNoAccount = errors.New("helper: no such account")

// The account database lookups are variables so tests can inject a failure
// that must not be mistaken for a missing account.
var (
	lookupUIDFromDB = accountdb.LookupUID
	groupIDsFromDB  = accountdb.GroupIDs
)

// LookupAccount reads the account database afresh on every call.
func LookupAccount(uid uint32) (Account, error) {
	u, err := lookupUIDFromDB(uid)
	if err != nil {
		if errors.Is(err, accountdb.ErrNotFound) {
			return Account{}, fmt.Errorf("%w: uid %d", ErrNoAccount, uid)
		}
		return Account{}, fmt.Errorf("helper: looking up uid %d: %w", uid, err)
	}

	groups, err := groupIDsFromDB(u)
	if err != nil {
		if errors.Is(err, accountdb.ErrNotFound) {
			return Account{}, fmt.Errorf("%w: uid %d", ErrNoAccount, uid)
		}
		return Account{}, fmt.Errorf("helper: account %q: supplementary groups: %w", u.Name, err)
	}

	return Account{
		Name:   u.Name,
		UID:    u.UID,
		GID:    u.GID,
		Groups: groups,
		Home:   u.Home,
	}, nil
}

// parseID converts a decimal group or user id handed over in the environment.
func parseID(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// accountCommand returns a command that runs exe with args as acct. The
// account-helper needs the account's full identity, including its saved uid, so
// the credential is changed before exec.
func accountCommand(exe string, args []string, acct Account) *exec.Cmd {
	cmd := commandFor(exe, args, acct)
	cmd.Stderr = os.Stderr
	if acct.UID != uint32(os.Geteuid()) {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:    acct.UID,
			Gid:    acct.GID,
			Groups: acct.Groups,
		}
	}
	return cmd
}

// accessCheckCommand returns a command that runs exe's access-check subcommand.
// It execs as root and carries the account identity in the environment: the
// child drops only its effective identity, so the account that is being checked
// never owns the process or its pipes. Its stderr is left nil, which connects
// it to /dev/null; the framed response on stdout is the only output it needs,
// and discarding stderr removes a channel for injecting Broker log lines.
func accessCheckCommand(exe string, acct Account) *exec.Cmd {
	cmd := commandFor(exe, []string{"internal", "access-check"}, acct)
	cmd.Env = append(cmd.Env,
		accessCheckUIDEnv+"="+strconv.FormatUint(uint64(acct.UID), 10),
		accessCheckGIDEnv+"="+strconv.FormatUint(uint64(acct.GID), 10),
		accessCheckGroupsEnv+"="+joinIDs(acct.Groups),
	)
	return cmd
}

// commandFor builds the environment and process attributes shared by the
// helper children. It does not change credentials; callers decide whether the
// child execs as the Broker or as an account.
func commandFor(exe string, args []string, acct Account) *exec.Cmd {
	cmd := &exec.Cmd{
		Path: exe,
		Args: append([]string{exe}, args...),
		Env: []string{
			"HOME=" + acct.Home,
			"USER=" + acct.Name,
			"LOGNAME=" + acct.Name,
			"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		},
		Dir:         "/",
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	setParentDeathSignal(cmd.SysProcAttr)
	return cmd
}

// joinIDs renders a group id list for the environment, comma separated.
func joinIDs(ids []uint32) string {
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(id), 10))
	}
	return b.String()
}
