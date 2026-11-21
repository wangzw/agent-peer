package helper

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"slices"
	"strconv"
	"testing"

	"github.com/wangzw/peers/internal/accountdb"
)

func TestLookupAccountTemporaryFailureIsNotMissing(t *testing.T) {
	oldUID, oldGroups := lookupUIDFromDB, groupIDsFromDB
	t.Cleanup(func() { lookupUIDFromDB, groupIDsFromDB = oldUID, oldGroups })

	lookupUIDFromDB = func(uint32) (accountdb.User, error) {
		return accountdb.User{Name: "alice", UID: 7, GID: 9, Home: "/home/alice"}, nil
	}
	groupIDsFromDB = func(accountdb.User) ([]uint32, error) {
		return nil, errors.New("account database unavailable")
	}

	_, err := LookupAccount(7)
	if err == nil {
		t.Fatal("LookupAccount succeeded with a failing group lookup")
	}
	if errors.Is(err, ErrNoAccount) {
		t.Errorf("LookupAccount error = %v, want a failed check, not ErrNoAccount", err)
	}
}

func TestLookupAccountMissingUIDIsNoAccount(t *testing.T) {
	old := lookupUIDFromDB
	t.Cleanup(func() { lookupUIDFromDB = old })

	lookupUIDFromDB = func(uid uint32) (accountdb.User, error) {
		return accountdb.User{}, fmt.Errorf("%w: uid %d", accountdb.ErrNotFound, uid)
	}

	_, err := LookupAccount(7)
	if !errors.Is(err, ErrNoAccount) {
		t.Errorf("LookupAccount error = %v, want ErrNoAccount", err)
	}
}

func TestLookupAccountCurrentUser(t *testing.T) {
	uid := uint32(os.Getuid())
	acct, err := LookupAccount(uid)
	if err != nil {
		t.Fatalf("LookupAccount(%d): %v", uid, err)
	}
	if acct.UID != uid {
		t.Errorf("UID = %d, want %d", acct.UID, uid)
	}
	if gid := uint32(os.Getgid()); acct.GID != gid {
		t.Errorf("GID = %d, want %d", acct.GID, gid)
	}
	if acct.Name == "" {
		t.Error("Name is empty")
	}
	if !slices.Contains(acct.Groups, acct.GID) {
		t.Errorf("Groups = %v, want it to contain the primary group %d", acct.Groups, acct.GID)
	}
}

func TestLookupAccountUnknownUID(t *testing.T) {
	const first, last = uint32(0x7ffffff0), uint32(0x7ffffffe)
	for uid := first; uid <= last; uid++ {
		if _, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
			continue
		}
		_, err := LookupAccount(uid)
		if !errors.Is(err, ErrNoAccount) {
			t.Fatalf("LookupAccount(%d) error = %v, want ErrNoAccount", uid, err)
		}
		return
	}
	t.Skip("every probed uid exists in the account database")
}

func TestAccountCommandEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/0")
	t.Setenv("TMPDIR", "/tmp/root")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/s")

	acct := Account{
		Name: "alice",
		UID:  uint32(os.Geteuid()),
		GID:  1000,
		Home: "/home/alice",
	}
	cmd := accountCommand("/usr/bin/agent-peer", []string{"internal", "account-helper"}, acct)

	want := []string{
		"HOME=/home/alice",
		"USER=alice",
		"LOGNAME=alice",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}
	if !slices.Equal(cmd.Env, want) {
		t.Errorf("Env = %q, want %q", cmd.Env, want)
	}
	if cmd.Dir != "/" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/")
	}
}

func TestAccountCommandCredentialOnlyForOtherAccount(t *testing.T) {
	euid := uint32(os.Geteuid())

	self := Account{
		Name:   "self",
		UID:    euid,
		GID:    7,
		Groups: []uint32{7, 9},
		Home:   "/home/self",
	}
	cmd := accountCommand("/bin/x", nil, self)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if cmd.SysProcAttr.Credential != nil {
		t.Errorf("Credential = %+v, want nil for the current account", cmd.SysProcAttr.Credential)
	}

	otherUID := euid + 1
	other := Account{
		Name:   "other",
		UID:    otherUID,
		GID:    11,
		Groups: []uint32{11, 12},
		Home:   "/home/other",
	}
	cmd = accountCommand("/bin/x", nil, other)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil {
		t.Fatal("Credential is nil, want the other account")
	}
	if cred.Uid != otherUID {
		t.Errorf("Credential.Uid = %d, want %d", cred.Uid, otherUID)
	}
	if cred.Gid != other.GID {
		t.Errorf("Credential.Gid = %d, want %d", cred.Gid, other.GID)
	}
	if !slices.Equal(cred.Groups, other.Groups) {
		t.Errorf("Credential.Groups = %v, want %v", cred.Groups, other.Groups)
	}
}
