package helper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"syscall"
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

// LookupAccount reads the account database afresh on every call.
func LookupAccount(uid uint32) (Account, error) {
	id := strconv.FormatUint(uint64(uid), 10)
	u, err := user.LookupId(id)
	if err != nil {
		var unknown user.UnknownUserIdError
		if errors.As(err, &unknown) {
			return Account{}, fmt.Errorf("%w: uid %s", ErrNoAccount, id)
		}
		return Account{}, fmt.Errorf("helper: looking up uid %s: %w", id, err)
	}

	gid, err := parseID(u.Gid)
	if err != nil {
		return Account{}, fmt.Errorf("helper: account %q: primary group id %q: %w", u.Username, u.Gid, err)
	}

	groupIDs, err := u.GroupIds()
	if err != nil {
		return Account{}, fmt.Errorf("helper: account %q: supplementary groups: %w", u.Username, err)
	}
	groups := make([]uint32, 0, len(groupIDs)+1)
	seen := make(map[uint32]struct{}, len(groupIDs)+1)
	for _, g := range groupIDs {
		groupUID, err := parseID(g)
		if err != nil {
			return Account{}, fmt.Errorf("helper: account %q: supplementary group id %q: %w", u.Username, g, err)
		}
		if _, ok := seen[groupUID]; ok {
			continue
		}
		seen[groupUID] = struct{}{}
		groups = append(groups, groupUID)
	}
	if _, ok := seen[gid]; !ok {
		groups = append(groups, gid)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })

	return Account{
		Name:   u.Username,
		UID:    uid,
		GID:    gid,
		Groups: groups,
		Home:   u.HomeDir,
	}, nil
}

// parseID converts a decimal group or user id from the account database.
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
