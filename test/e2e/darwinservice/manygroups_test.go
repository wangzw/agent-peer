//go:build e2e_darwin

package darwinservice

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/wangzw/peers/internal/broker/helper"
	"github.com/wangzw/peers/internal/broker/helperpb"
	"github.com/wangzw/peers/test/e2e/node"
)

const (
	// accessCheckRole runs the access-check helper in-process. It is dispatched
	// from TestMain before the test binary tries to build and install anything.
	accessCheckRole = "access-check-many-groups"
	// getConfigRole calls GetConfig on the system Broker's local socket.
	getConfigRole = "get-config"

	manyGroupUser  = "approbe"
	manyGroupUID   = 6501
	manyGroupGID   = 6101
	manyGroupCount = 20
	negativeGroup  = "apneg"
	negativeGID    = 6199
	manyGroupDir   = "/tmp/peers-manygroups"

	// systemConfigPath is the file the system Broker checks for every
	// permission-gated call.
	systemConfigPath = "/etc/peers/config.yaml"

	// manyGroupsWaitBound bounds the wait for the directory service to report a
	// freshly created account's group memberships on a loaded CI runner.
	manyGroupsWaitBound = 30 * time.Second
	// getConfigTimeout bounds one get_config call to the local Broker.
	getConfigTimeout = 15 * time.Second

	// accessCheckUIDEnv and friends are the helper's identity environment,
	// written by the Broker when it derives the check process.
	accessCheckUIDEnv    = "PEERS_ACCESS_CHECK_UID"
	accessCheckGIDEnv    = "PEERS_ACCESS_CHECK_GID"
	accessCheckGroupsEnv = "PEERS_ACCESS_CHECK_GROUPS"
)

// manyGroupFixture is the account and the two permission fixtures.
type manyGroupFixture struct {
	uid     int
	gid     int
	groups  []uint32
	group18 string
	allow   string
	deny    string
}

// TestAccessCheckManyGroups reproduces the Darwin failure before the fix: the
// account is in more groups than NGROUPS, and the read permission comes from
// the eighteenth group. The helper must switch to the account and allow the
// check; the negative fixture must stay denied.
func TestAccessCheckManyGroups(t *testing.T) {
	f := setupManyGroups(t)
	env := []string{
		accessCheckUIDEnv + "=" + strconv.Itoa(f.uid),
		accessCheckGIDEnv + "=" + strconv.Itoa(f.gid),
		accessCheckGroupsEnv + "=" + joinGroupIDs(f.groups),
	}

	res := runRoleAsRoot(t, accessCheckRole, env, f.allow)
	if res.exitCode != 0 {
		t.Fatalf("access check as the %d-group account: exit %d\nstdout: %s\nstderr: %s",
			len(f.groups), res.exitCode, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "allowed=true") {
		t.Errorf("access check on the allowed fixture: %s, want allowed=true", res.stdout)
	}
	if !strings.Contains(res.stdout, "error=\"\"") {
		t.Errorf("access check on the allowed fixture: %s, want an empty error", res.stdout)
	}
	if !strings.Contains(res.stdout, "uid="+strconv.Itoa(f.uid)) {
		t.Errorf("access check on the allowed fixture: %s, want uid=%d", res.stdout, f.uid)
	}

	res = runRoleAsRoot(t, accessCheckRole, env, f.deny)
	if res.exitCode != 0 {
		t.Fatalf("access check on the denied fixture: exit %d\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "allowed=false") {
		t.Errorf("access check on the denied fixture: %s, want allowed=false", res.stdout)
	}
	if !strings.Contains(res.stdout, "error=\"\"") {
		t.Errorf("access check on the denied fixture: %s, want an empty error, not a failed check", res.stdout)
	}
}

// TestSystemServiceManyGroups is the end-to-end acceptance: the installed
// system Broker serves get_config to an account in more than NGROUPS when the
// config file grants one of its groups read access, and returns
// permission_denied once that grant is removed.
func TestSystemServiceManyGroups(t *testing.T) {
	f := setupManyGroups(t)
	resetSystemService(t)
	t.Cleanup(func() {
		resetSystemService(t)
		restoreSystemConfig(t)
	})

	ensureSystemConfig(t)
	grantSystemConfigRead(t, f.group18)

	res := run(t, "sudo", "-n", installedBinary, "service", "install", "--system")
	if res.exitCode != 0 {
		t.Fatalf("service install --system: exit %d\nstdout: %s\nstderr: %s",
			res.exitCode, res.stdout, res.stderr)
	}
	waitForPID(t, "system", serviceStartBound)

	out := runRoleAsUser(t, manyGroupUser, getConfigRole)
	if out.exitCode != 0 {
		t.Fatalf("get_config as the %d-group account: exit %d\nstdout: %s\nstderr: %s",
			len(f.groups), out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stdout, "RESULT entries=") {
		t.Errorf("get_config as the %d-group account: %s, want a successful response", len(f.groups), out.stdout)
	}

	// Revoke the group's read permission. The access check itself must still
	// succeed, so the answer is permission_denied, not internal.
	mustSudo(t, "chmod", "0600", systemConfigPath)
	out = runRoleAsUser(t, manyGroupUser, getConfigRole)
	if out.exitCode == 0 {
		t.Fatalf("get_config after revoking read: got success, want permission_denied\nstdout: %s", out.stdout)
	}
	if !strings.Contains(out.stdout, "code=PermissionDenied") {
		t.Errorf("get_config after revoking read: %s, want code=PermissionDenied", out.stdout)
	}
}

// runAccessCheckRole is the role body: it builds one AccessCheckRequest, runs
// the real helper and prints the result. It runs as root through sudo.
func runAccessCheckRole() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "access-check-many-groups: missing path")
		return 2
	}
	var in bytes.Buffer
	req := &helperpb.AccessCheckRequest{
		ProtocolVersion: helperpb.ProtocolVersion,
		Path:            os.Args[1],
		Access:          helperpb.Access_ACCESS_READ,
	}
	if _, err := protodelim.MarshalTo(&in, req); err != nil {
		fmt.Printf("RESULT error=%q\n", err.Error())
		return 1
	}
	var out bytes.Buffer
	if err := helper.RunAccessCheck(&in, &out); err != nil {
		fmt.Printf("RESULT error=%q\n", err.Error())
		return 1
	}
	var resp helperpb.AccessCheckResponse
	if err := (protodelim.UnmarshalOptions{MaxSize: 4 << 20}).UnmarshalFrom(bufio.NewReader(&out), &resp); err != nil {
		fmt.Printf("RESULT error=%q\n", err.Error())
		return 1
	}
	fmt.Printf("RESULT allowed=%t uid=%d error=%q\n", resp.GetAllowed(), resp.GetUid(), resp.GetError())
	return 0
}

// runGetConfigRole is the role body: it calls GetConfig as the invoking
// account and prints the answer or the gRPC status code.
func runGetConfigRole() int {
	oracle, err := node.DialOracle(systemSocketPath)
	if err != nil {
		fmt.Printf("RESULT error=%q\n", err.Error())
		return 1
	}
	defer func() { _ = oracle.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), getConfigTimeout)
	defer cancel()
	resp, err := oracle.GetConfig(ctx, "")
	if err != nil {
		fmt.Printf("RESULT code=%s error=%q\n", status.Code(err), err.Error())
		return 1
	}
	fmt.Printf("RESULT entries=%d\n", len(resp.GetEntries()))
	return 0
}

// setupManyGroups creates the account, its groups and the two fixtures, and
// registers their removal.
func setupManyGroups(t *testing.T) *manyGroupFixture {
	t.Helper()
	preflightManyGroupIDs(t)
	cleanupManyGroups(t)
	t.Cleanup(func() { cleanupManyGroups(t) })

	for i := 1; i <= manyGroupCount; i++ {
		name := fmt.Sprintf("apg%02d", i)
		gid := 6100 + i
		sudoIgnore("dscl", ".", "-create", "/Groups/"+name)
		mustSudo(t, "dscl", ".", "-create", "/Groups/"+name, "PrimaryGroupID", strconv.Itoa(gid))
		sudoIgnore("dscl", ".", "-create", "/Groups/"+name, "RealName", "peers e2e group")
	}
	sudoIgnore("dscl", ".", "-create", "/Groups/"+negativeGroup)
	mustSudo(t, "dscl", ".", "-create", "/Groups/"+negativeGroup, "PrimaryGroupID", strconv.Itoa(negativeGID))

	sudoIgnore("dscl", ".", "-create", "/Users/"+manyGroupUser)
	mustSudo(t, "dscl", ".", "-create", "/Users/"+manyGroupUser, "UniqueID", strconv.Itoa(manyGroupUID))
	mustSudo(t, "dscl", ".", "-create", "/Users/"+manyGroupUser, "PrimaryGroupID", strconv.Itoa(manyGroupGID))
	sudoIgnore("dscl", ".", "-create", "/Users/"+manyGroupUser, "UserShell", "/bin/bash")
	sudoIgnore("dscl", ".", "-create", "/Users/"+manyGroupUser, "NFSHomeDirectory", "/var/empty")

	for i := 1; i <= manyGroupCount; i++ {
		name := fmt.Sprintf("apg%02d", i)
		sudoIgnore("dscl", ".", "-delete", "/Groups/"+name, "GroupMembership", manyGroupUser)
		mustSudo(t, "dscl", ".", "-append", "/Groups/"+name, "GroupMembership", manyGroupUser)
	}
	sudoIgnore("dscacheutil", "-flushcache")

	groups := waitForGroups(t, manyGroupUser, manyGroupCount)
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	group18 := groupNameForGID(t, groups[17])
	if !strings.HasPrefix(group18, "apg") {
		t.Fatalf("the eighteenth group is %q, not one of the probe groups", group18)
	}

	f := &manyGroupFixture{
		uid:     manyGroupUID,
		gid:     manyGroupGID,
		groups:  groups,
		group18: group18,
		allow:   filepath.Join(manyGroupDir, "allow.txt"),
		deny:    filepath.Join(manyGroupDir, "deny.txt"),
	}
	f.writeFiles(t)
	t.Logf("account %s uid=%d gid=%d groups=%v; eighteenth group %s",
		manyGroupUser, f.uid, f.gid, f.groups, f.group18)
	return f
}

// writeFiles creates the allowed fixture under the eighteenth group and the
// denied fixture under a group the account is not in.
func (f *manyGroupFixture) writeFiles(t *testing.T) {
	t.Helper()
	mustSudo(t, "install", "-d", "-o", "root", "-g", "wheel", "-m", "0755", manyGroupDir)
	writeFixtureFile(t, "allowed through the eighteenth group", f.allow, f.group18)
	writeFixtureFile(t, "denied through a foreign group", f.deny, negativeGroup)
}

func writeFixtureFile(t *testing.T, content, dest, group string) {
	t.Helper()
	tmp, err := os.CreateTemp("", "peers-manygroups-*")
	if err != nil {
		t.Fatalf("creating a fixture source: %v", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		t.Fatalf("writing a fixture source: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("closing a fixture source: %v", err)
	}
	mustSudo(t, "install", "-o", "root", "-g", group, "-m", "0640", tmp.Name(), dest)
}

// cleanupManyGroups removes the account, the groups and the fixtures. It is
// idempotent and also runs before setup, so a rerun in the same job starts
// clean.
func cleanupManyGroups(t *testing.T) {
	t.Helper()
	for _, name := range []string{"allow.txt", "deny.txt"} {
		sudoIgnore("rm", "-f", filepath.Join(manyGroupDir, name))
	}
	sudoIgnore("rmdir", manyGroupDir)
	sudoIgnore("dscl", ".", "-delete", "/Users/"+manyGroupUser)
	for i := 1; i <= manyGroupCount; i++ {
		sudoIgnore("dscl", ".", "-delete", fmt.Sprintf("/Groups/apg%02d", i))
	}
	sudoIgnore("dscl", ".", "-delete", "/Groups/"+negativeGroup)
	sudoIgnore("dscacheutil", "-flushcache")
}

// preflightManyGroupIDs refuses to touch a name or id that belongs to another
// account. A stale probe account with the expected id is fine; cleanup removes
// it afterwards.
func preflightManyGroupIDs(t *testing.T) {
	t.Helper()
	users := dsclList(t, "/Users", "UniqueID")
	groups := dsclList(t, "/Groups", "PrimaryGroupID")
	checkNameID(t, users, manyGroupUser, manyGroupUID, "user", "uid")
	for i := 1; i <= manyGroupCount; i++ {
		checkNameID(t, groups, fmt.Sprintf("apg%02d", i), 6100+i, "group", "gid")
	}
	checkNameID(t, groups, negativeGroup, negativeGID, "group", "gid")
}

// dsclList reads name-to-id pairs from one local directory record type.
func dsclList(t *testing.T, record, attr string) map[string]uint32 {
	t.Helper()
	out, err := exec.Command("sudo", "-n", "dscl", ".", "-list", record, attr).Output()
	if err != nil {
		t.Fatalf("listing %s %s: %v", record, attr, err)
	}
	ids := make(map[string]uint32)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			continue
		}
		ids[fields[0]] = uint32(v)
	}
	return ids
}

// checkNameID fails when name already has a different id or when id belongs to
// a different name.
func checkNameID(t *testing.T, ids map[string]uint32, name string, want int, kind, attr string) {
	t.Helper()
	for other, id := range ids {
		if id == uint32(want) && other != name {
			t.Fatalf("%s %q already uses %s %d", kind, other, attr, want)
		}
	}
	if got, ok := ids[name]; ok && got != uint32(want) {
		t.Fatalf("%s %q exists with %s %d, want %d", kind, name, attr, got, want)
	}
}

// waitForGroups waits for the directory service to report at least want groups
// for user.
func waitForGroups(t *testing.T, user string, want int) []uint32 {
	t.Helper()
	deadline := time.Now().Add(manyGroupsWaitBound)
	for {
		out, err := exec.Command("id", "-G", user).Output()
		if err == nil {
			ids, parseErr := parseGroupIDs(string(out))
			if parseErr == nil && len(ids) >= want {
				return ids
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the account %s did not reach %d groups: %v", user, want, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func parseGroupIDs(s string) ([]uint32, error) {
	fields := strings.Fields(s)
	ids := make([]uint32, 0, len(fields))
	for _, field := range fields {
		v, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", field, err)
		}
		ids = append(ids, uint32(v))
	}
	return ids, nil
}

// groupNameForGID resolves a group id through the local directory node.
func groupNameForGID(t *testing.T, gid uint32) string {
	t.Helper()
	out, err := exec.Command("sudo", "-n", "dscl", ".", "-list", "/Groups", "PrimaryGroupID").Output()
	if err != nil {
		t.Fatalf("listing groups: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 32)
		if err == nil && uint32(v) == gid {
			return fields[0]
		}
	}
	t.Fatalf("no group has gid %d", gid)
	return ""
}

// grantSystemConfigRead gives group read access to the system configuration.
func grantSystemConfigRead(t *testing.T, group string) {
	t.Helper()
	mustSudo(t, "chgrp", group, systemConfigPath)
	mustSudo(t, "chmod", "0640", systemConfigPath)
}

// restoreSystemConfig returns the system configuration to its installed mode.
func restoreSystemConfig(t *testing.T) {
	t.Helper()
	if err := sudo("chown", "root:wheel", systemConfigPath); err != nil {
		t.Logf("restoring the system configuration owner: %v", err)
	}
	if err := sudo("chmod", "0600", systemConfigPath); err != nil {
		t.Logf("restoring the system configuration mode: %v", err)
	}
}

// runRoleAsRoot runs the e2e binary role through sudo.
func runRoleAsRoot(t *testing.T, role string, env []string, args ...string) result {
	t.Helper()
	cmdArgs := []string{"-n", "env", roleEnv + "=" + role}
	cmdArgs = append(cmdArgs, env...)
	cmdArgs = append(cmdArgs, e2eBinary)
	cmdArgs = append(cmdArgs, args...)
	return run(t, "sudo", cmdArgs...)
}

// runRoleAsUser runs the e2e binary role as user through sudo.
func runRoleAsUser(t *testing.T, user, role string, args ...string) result {
	t.Helper()
	cmdArgs := []string{"-n", "-u", user, "env", roleEnv + "=" + role, e2eBinary}
	cmdArgs = append(cmdArgs, args...)
	return run(t, "sudo", cmdArgs...)
}

// joinGroupIDs renders a group id list for the helper environment.
func joinGroupIDs(ids []uint32) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(parts, ",")
}

// mustSudo runs one command through sudo and fails the test on error.
func mustSudo(t *testing.T, args ...string) {
	t.Helper()
	if err := sudo(args...); err != nil {
		t.Fatalf("%v", err)
	}
}

// sudoIgnore runs one command through sudo and discards its result. It is for
// idempotent setup and cleanup steps.
func sudoIgnore(args ...string) {
	_ = sudo(args...)
}
