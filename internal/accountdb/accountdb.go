// Package accountdb looks up accounts and groups in the system account
// database. On Linux that is what the C library's NSS configuration
// resolves, so entries from sources such as LDAP, SSSD or extrausers are
// visible; /etc/passwd and /etc/group alone are not treated as the whole
// database.
//
// The package depends only on the standard library.
package accountdb

import (
	"errors"
	"fmt"
	"os/user"
	"sort"
	"strconv"
)

// User is one entry of the account database.
type User struct {
	Name string
	UID  uint32
	GID  uint32
	Home string
}

// ErrNotFound reports that the account database has no entry for the request.
var ErrNotFound = errors.New("accountdb: not found")

// LookupUID returns the account with the given uid.
func LookupUID(uid uint32) (User, error) { return lookupUID(uid) }

// LookupName returns the account with the given name.
func LookupName(name string) (User, error) { return lookupName(name) }

// LookupGroup returns the gid of the group with the given name.
func LookupGroup(name string) (gid uint32, err error) { return lookupGroup(name) }

// GroupIDs returns the account's groups, supplementary groups and primary
// group together, sorted and without duplicates.
func GroupIDs(u User) ([]uint32, error) { return groupIDs(u) }

// parseDecimal32 parses a decimal 32-bit id. Only digits are accepted, so a
// sign or surrounding space cannot slip through.
func parseDecimal32(s string) (uint32, error) {
	if s == "" {
		return 0, errors.New("empty id")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("id %q is not decimal", s)
		}
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// normalizeGroupIDs adds the primary gid, removes duplicates and sorts the
// result.
func normalizeGroupIDs(primary uint32, ids []uint32) []uint32 {
	seen := make(map[uint32]struct{}, len(ids)+1)
	out := make([]uint32, 0, len(ids)+1)
	add := func(id uint32) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	add(primary)
	for _, id := range ids {
		add(id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// The os/user implementations serve as the platform implementation outside
// Linux and as the fallback on Linux. With CGO_ENABLED=0 they read only
// /etc/passwd and /etc/group.
func osUserLookupUID(uid uint32) (User, error) {
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		var unknown user.UnknownUserIdError
		if errors.As(err, &unknown) {
			return User{}, fmt.Errorf("%w: uid %d", ErrNotFound, uid)
		}
		return User{}, fmt.Errorf("accountdb: looking up uid %d: %w", uid, err)
	}
	return osUserToUser(u)
}

func osUserLookupName(name string) (User, error) {
	u, err := user.Lookup(name)
	if err != nil {
		var unknown user.UnknownUserError
		if errors.As(err, &unknown) {
			return User{}, fmt.Errorf("%w: user %s", ErrNotFound, name)
		}
		return User{}, fmt.Errorf("accountdb: looking up user %s: %w", name, err)
	}
	return osUserToUser(u)
}

func osUserLookupGroup(name string) (uint32, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		var unknown user.UnknownGroupError
		if errors.As(err, &unknown) {
			return 0, fmt.Errorf("%w: group %s", ErrNotFound, name)
		}
		return 0, fmt.Errorf("accountdb: looking up group %s: %w", name, err)
	}
	gid, err := parseDecimal32(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("accountdb: group %s has an invalid gid %q: %w", name, g.Gid, err)
	}
	return gid, nil
}

func osUserGroupIDs(u User) ([]uint32, error) {
	account, err := user.Lookup(u.Name)
	if err != nil {
		var unknown user.UnknownUserError
		if errors.As(err, &unknown) {
			return nil, fmt.Errorf("%w: user %s", ErrNotFound, u.Name)
		}
		return nil, fmt.Errorf("accountdb: looking up user %s: %w", u.Name, err)
	}
	// The local source may hold a different account with the same name. Its
	// groups belong to that account, not to the one being looked up.
	uid, err := parseDecimal32(account.Uid)
	if err != nil {
		return nil, fmt.Errorf("accountdb: account %s has an invalid uid %q: %w", u.Name, account.Uid, err)
	}
	if uid != u.UID {
		return nil, fmt.Errorf("accountdb: user %q has uid %d, want %d", u.Name, uid, u.UID)
	}
	ids, err := account.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("accountdb: supplementary groups of %s: %w", u.Name, err)
	}
	parsed := make([]uint32, 0, len(ids))
	for _, id := range ids {
		v, err := parseDecimal32(id)
		if err != nil {
			return nil, fmt.Errorf("accountdb: account %s: group id %q: %w", u.Name, id, err)
		}
		parsed = append(parsed, v)
	}
	return normalizeGroupIDs(u.GID, parsed), nil
}

func osUserToUser(u *user.User) (User, error) {
	uid, err := parseDecimal32(u.Uid)
	if err != nil {
		return User{}, fmt.Errorf("accountdb: account %s has an invalid uid %q: %w", u.Username, u.Uid, err)
	}
	gid, err := parseDecimal32(u.Gid)
	if err != nil {
		return User{}, fmt.Errorf("accountdb: account %s has an invalid gid %q: %w", u.Username, u.Gid, err)
	}
	return User{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir}, nil
}
