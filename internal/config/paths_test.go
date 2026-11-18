package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	base := t.TempDir()
	homeErr := errors.New("no home")
	wantRoot := "/run/agent-peer"
	if runtime.GOOS == "darwin" {
		wantRoot = "/var/run/agent-peer"
	}

	tests := []struct {
		name    string
		mode    Mode
		home    string
		homeErr error
		euid    int
		want    PathSet
		wantErr bool
	}{
		{
			name: "user mode uses the home directory",
			mode: ModeUser,
			home: base,
			euid: 1000,
			want: PathSet{
				RuntimeDir: runtimeDirPath(1000),
				ConfigFile: filepath.Join(base, ".agent-peer", "config.yaml"),
				DataDir:    filepath.Join(base, ".agent-peer"),
				AuditLog:   filepath.Join(base, ".agent-peer", "audit.log"),
			},
		},
		{
			name: "system mode uses fixed paths",
			mode: ModeSystem,
			euid: 0,
			want: PathSet{
				RuntimeDir: wantRoot,
				ConfigFile: "/etc/agent-peer/config.yaml",
				DataDir:    "/var/lib/agent-peer",
				AuditLog:   "/var/log/agent-peer/audit.log",
			},
		},
		{
			name:    "home lookup failure",
			mode:    ModeUser,
			homeErr: homeErr,
			euid:    1000,
			wantErr: true,
		},
		{
			name:    "empty home directory",
			mode:    ModeUser,
			home:    "",
			euid:    1000,
			wantErr: true,
		},
		{
			name:    "unknown mode",
			mode:    Mode(99),
			euid:    1000,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &core{
				mode: tc.mode,
				euid: func() int { return tc.euid },
				homeDir: func() (string, error) {
					return tc.home, tc.homeErr
				},
			}
			got, err := c.resolvePaths()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolvePaths() = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePaths() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolvePaths() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRuntimeDirPath(t *testing.T) {
	wantRoot := "/run/agent-peer"
	if runtime.GOOS == "darwin" {
		wantRoot = "/var/run/agent-peer"
	}
	if got := runtimeDirPath(0); got != wantRoot {
		t.Errorf("runtimeDirPath(0) = %q, want %q", got, wantRoot)
	}
	if got := runtimeDirPath(1000); got != "/tmp/agent-peer-1000" {
		t.Errorf("runtimeDirPath(1000) = %q, want /tmp/agent-peer-1000", got)
	}
}

// REQ-broker-config-013: the account lock path depends only on the account,
// never on the deployment mode, home directory, configuration or listen
// address.
func TestREQBrokerConfig013_AccountLockPathDependsOnAccount(t *testing.T) {
	first := accountLockPath(1000)
	second := accountLockPath(1000)
	other := accountLockPath(1001)

	if first != second {
		t.Fatalf("accountLockPath is not deterministic: %q != %q", first, second)
	}
	if first == other {
		t.Fatalf("accountLockPath(1000) and accountLockPath(1001) are both %q", first)
	}
	if filepath.Base(first) != "broker.lock" {
		t.Fatalf("accountLockPath(1000) = %q, want a broker.lock path", first)
	}
	if first != filepath.Join(runtimeDirPath(1000), "broker.lock") {
		t.Fatalf("accountLockPath(1000) = %q, want it under the runtime directory", first)
	}
}

func TestCheckRuntimeDir_NonRootPermissionRule(t *testing.T) {
	tests := []struct {
		perm    os.FileMode
		wantErr bool
	}{
		{0o700, false},
		{0o500, false},
		{0o750, true},
		{0o710, true},
		{0o701, true},
		{0o755, true},
	}
	for _, tc := range tests {
		err := checkDirPermission(tc.perm, 1000, permRuntime)
		if tc.wantErr && err == nil {
			t.Errorf("checkDirPermission(%#o, non-root, runtime) = nil, want an error", tc.perm)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("checkDirPermission(%#o, non-root, runtime) = %v, want nil", tc.perm, err)
		}
	}
}

func TestCheckRuntimeDir_RootPermissionRule(t *testing.T) {
	// A root runtime directory is 0755 but may be tightened. Group or world
	// write access is the only forbidden part, because root's directory is
	// shared by the system Broker and root's user-level Broker.
	tests := []struct {
		perm    os.FileMode
		wantErr bool
	}{
		{0o755, false},
		{0o750, false},
		{0o700, false},
		{0o775, true},
		{0o757, true},
		{0o777, true},
	}
	for _, tc := range tests {
		err := checkDirPermission(tc.perm, 0, permRuntime)
		if tc.wantErr && err == nil {
			t.Errorf("checkDirPermission(%#o, root, runtime) = nil, want an error", tc.perm)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("checkDirPermission(%#o, root, runtime) = %v, want nil", tc.perm, err)
		}
	}
}

func TestCheckRuntimeDir_DataAndSharedRules(t *testing.T) {
	if err := checkDirPermission(0o700, 1000, permData); err != nil {
		t.Errorf("private directory 0700 rejected: %v", err)
	}
	if err := checkDirPermission(0o750, 1000, permData); err == nil {
		t.Error("private directory 0750 accepted, want a rejection")
	}
	if err := checkDirPermission(0o755, 0, permShared); err != nil {
		t.Errorf("shared directory 0755 rejected: %v", err)
	}
	if err := checkDirPermission(0o775, 0, permShared); err == nil {
		t.Error("shared directory 0775 accepted, want a rejection")
	}
}

func TestCheckRuntimeDir_RejectsWrongOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	other := 1
	if os.Geteuid() == 1 {
		other = 2
	}
	if other == os.Geteuid() {
		other = os.Geteuid() + 1
	}
	if err := validateDir(dir, other, permRuntime); err == nil {
		t.Fatalf("validateDir accepted a directory owned by uid %d, asked for %d", os.Geteuid(), other)
	} else if !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("validateDir error = %v, want it to name the owner mismatch", err)
	}
}

func TestCheckRuntimeDir_RejectsSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := validateDir(link, os.Geteuid(), permRuntime); err == nil {
		t.Fatal("validateDir accepted a symbolic link")
	} else if !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("validateDir error = %v, want it to mention a symbolic link", err)
	}
}

func TestCheckRuntimeDir_RejectsNonDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDir(file, os.Geteuid(), permRuntime); err == nil {
		t.Fatal("validateDir accepted a regular file")
	} else if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("validateDir error = %v, want it to mention that it is not a directory", err)
	}
}

func TestCheckRuntimeDir_CreatesAndChecksDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "runtime")
	if err := ensureDir(dir, 0o700, os.Geteuid(), permData); err != nil {
		t.Fatalf("ensureDir() error = %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("new private directory has mode %#o, want no group or other bits", info.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(dir, 0o700, os.Geteuid(), permData); err == nil {
		t.Fatal("ensureDir accepted a directory readable by group and other")
	}
}

// withTemporaryRuntimeRoot points the runtime directory at root for one test.
// The seam exists so the account-lock path can be exercised without touching
// the real /run or /tmp directory of the account.
func withTemporaryRuntimeRoot(t *testing.T, root string) {
	t.Helper()
	old := runtimeRoot
	runtimeRoot = func(int) string { return root }
	t.Cleanup(func() { runtimeRoot = old })
}

// TestAccountLockTemporaryRoot covers the success path of the exported entry
// points under a temporary root: the directory is created, checked, and the
// lock path is directly beneath it.
func TestAccountLockTemporaryRoot(t *testing.T) {
	_ = takeAccountInitErr() // discard anything an earlier test left behind
	root := filepath.Join(t.TempDir(), "runtime")
	withTemporaryRuntimeRoot(t, root)

	if err := CheckRuntimeDir(); err != nil {
		t.Fatalf("CheckRuntimeDir() error = %v", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("lstat %s: %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", root)
	}
	if got, want := AccountLock(), filepath.Join(root, "broker.lock"); got != want {
		t.Fatalf("AccountLock() = %q, want %q", got, want)
	}
	if err := takeAccountInitErr(); err != nil {
		t.Fatalf("AccountLock recorded an error for a valid temporary root: %v", err)
	}
}

// TestAccountLockRuntimeDirFailureIsRemembered drives the failure branch with a
// temporary root whose parent is a regular file, so creating the directory
// fails. AccountLock must still return the account lock path and hand the
// failure to the next Load through takeAccountInitErr.
func TestAccountLockRuntimeDirFailureIsRemembered(t *testing.T) {
	_ = takeAccountInitErr() // discard anything an earlier test left behind
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(blocker, "runtime")
	withTemporaryRuntimeRoot(t, root)

	if err := CheckRuntimeDir(); err == nil {
		t.Fatal("CheckRuntimeDir() under a regular file returned no error")
	}

	if got, want := AccountLock(), filepath.Join(root, "broker.lock"); got != want {
		t.Fatalf("AccountLock() = %q, want %q even when the directory cannot be created", got, want)
	}
	err := takeAccountInitErr()
	if err == nil {
		t.Fatal("AccountLock did not remember the runtime-directory failure")
	}
	if !strings.Contains(err.Error(), "cannot create") {
		t.Fatalf("remembered error = %v, want it to name the failed directory creation", err)
	}
	if second := takeAccountInitErr(); second != nil {
		t.Fatalf("the remembered error is not consumed once: %v", second)
	}
}

// TestAccountLockRuntimeDirPermissionFailureIsRemembered covers the other way
// initializing the runtime directory fails: it exists but its mode violates the
// runtime rule. A root runtime directory may be group-readable but not group or
// world writable; a user runtime directory must not be accessible by group or
// other at all.
func TestAccountLockRuntimeDirPermissionFailureIsRemembered(t *testing.T) {
	_ = takeAccountInitErr() // discard anything an earlier test left behind
	root := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	forbidden := os.FileMode(0o750)
	if os.Geteuid() == 0 {
		forbidden = 0o777
	}
	if err := os.Chmod(root, forbidden); err != nil {
		t.Fatal(err)
	}
	withTemporaryRuntimeRoot(t, root)

	if err := CheckRuntimeDir(); err == nil {
		t.Fatalf("CheckRuntimeDir() accepted a runtime directory with mode %#o", forbidden)
	}
	if got, want := AccountLock(), filepath.Join(root, "broker.lock"); got != want {
		t.Fatalf("AccountLock() = %q, want %q", got, want)
	}
	if err := takeAccountInitErr(); err == nil {
		t.Fatal("AccountLock did not remember the permission failure")
	}
}
