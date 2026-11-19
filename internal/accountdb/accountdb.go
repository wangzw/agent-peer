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
	"log/slog"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
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

// errUnavailable reports that a lookup failed for a reason other than a
// missing entry: the database could not be read, or an answer could not be
// trusted. Callers must treat it as a failed check, not as a missing account.
var errUnavailable = errors.New("accountdb: account database unavailable")

// LookupUID returns the account with the given uid.
func LookupUID(uid uint32) (User, error) { return lookupUID(uid) }

// LookupName returns the account with the given name.
func LookupName(name string) (User, error) { return lookupName(name) }

// LookupGroup returns the gid of the group with the given name.
func LookupGroup(name string) (gid uint32, err error) { return lookupGroup(name) }

// GroupIDs returns the account's groups, supplementary groups and primary
// group together, sorted and without duplicates.
func GroupIDs(u User) ([]uint32, error) { return groupIDs(u) }

// validateName rejects names that must never reach a subprocess argument.
// Colon and whitespace are refused because they cannot occur in a sane account
// name and would change the meaning of a colon-separated or space-separated
// entry; NUL cannot be passed to exec at all.
func validateName(name string) error {
	if name == "" {
		return errors.New("accountdb: empty account name")
	}
	if strings.ContainsAny(name, ":\x00") || strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("accountdb: invalid account name %q", name)
	}
	return nil
}

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

// oneLine returns the single non-empty line of out. Everything else is an
// error: a lookup answering with several entries is not trusted, because the
// first line might not correspond to the request.
func oneLine(out []byte) (string, error) {
	s := string(out)
	line := strings.TrimSuffix(s, "\n")
	if line == "" {
		return "", errors.New("empty output")
	}
	if strings.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("output has %d lines, want one", strings.Count(line, "\n")+1)
	}
	return line, nil
}

// parsePasswd parses one getent passwd entry.
func parsePasswd(out []byte) (User, error) {
	line, err := oneLine(out)
	if err != nil {
		return User{}, fmt.Errorf("passwd entry: %w", err)
	}
	fields := strings.Split(line, ":")
	if len(fields) != 7 {
		return User{}, fmt.Errorf("passwd entry has %d fields, want 7", len(fields))
	}
	if err := validateName(fields[0]); err != nil {
		return User{}, err
	}
	uid, err := parseDecimal32(fields[2])
	if err != nil {
		return User{}, fmt.Errorf("passwd entry uid: %w", err)
	}
	gid, err := parseDecimal32(fields[3])
	if err != nil {
		return User{}, fmt.Errorf("passwd entry gid: %w", err)
	}
	home := fields[5]
	if !filepath.IsAbs(home) {
		return User{}, fmt.Errorf("passwd entry home %q is not absolute", home)
	}
	return User{Name: fields[0], UID: uid, GID: gid, Home: home}, nil
}

// parseGroup parses one getent group entry and returns its name and gid.
func parseGroup(out []byte) (string, uint32, error) {
	line, err := oneLine(out)
	if err != nil {
		return "", 0, fmt.Errorf("group entry: %w", err)
	}
	fields := strings.Split(line, ":")
	if len(fields) != 4 {
		return "", 0, fmt.Errorf("group entry has %d fields, want 4", len(fields))
	}
	name := fields[0]
	if err := validateName(name); err != nil {
		return "", 0, err
	}
	gid, err := parseDecimal32(fields[2])
	if err != nil {
		return "", 0, fmt.Errorf("group entry gid: %w", err)
	}
	return name, gid, nil
}

// parseInitgroups parses the single line of getent initgroups. The first field
// is the account name; the remaining fields are decimal gids.
func parseInitgroups(out []byte, name string) ([]uint32, error) {
	line, err := oneLine(out)
	if err != nil {
		return nil, fmt.Errorf("initgroups entry: %w", err)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != name {
		return nil, fmt.Errorf("initgroups entry does not start with %q", name)
	}
	ids := make([]uint32, 0, len(fields)-1)
	for _, field := range fields[1:] {
		id, err := parseDecimal32(field)
		if err != nil {
			return nil, fmt.Errorf("initgroups entry: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
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

// warnFallback records that the preferred lookup failed and os/user is used
// instead. The design requires this warning: falling back can make an account
// that the C library resolves invisible to the Broker.
func warnFallback(request string, err error) {
	slog.Warn("accountdb: account database lookup failed; falling back to os/user",
		"event", "accountdb_lookup_fallback", "request", request, "error", err.Error())
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
