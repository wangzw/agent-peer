package helper

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// switchRecorder records the identity operations and models the process
// credential, so a test can assert the order and see the verification.
type switchRecorder struct {
	calls []string
	gids  []uint32
	gmuid uint32

	realUID int
	effUID  int
	realGID int
	effGID  int

	// suppGroups is the supplementary group table getgroups reports. A real
	// switch replaces it in initgroups; groupsAfter overrides it for tests.
	suppGroups  []int
	groupsAfter []int

	// setuidDone lets the recorder tell the pre-switch root check from the
	// post-switch verification, which read geteuid at different moments.
	setuidDone bool

	uidAfter  *int // non-nil overrides the value the post-switch check reads
	euidAfter *int
	gidAfter  *int
	egidAfter *int

	// regainRoot models a process that kept a root identity: the post-switch
	// setuid(0) probe succeeds, which the switch must reject.
	regainRoot bool

	setgidErr     error
	initgroupsErr error
	setuidErr     error
	getgroupsErr  error
}

func newSwitchRecorder() *switchRecorder { return &switchRecorder{} }

func (r *switchRecorder) ops() helperIdentityOps {
	return helperIdentityOps{
		geteuid: func() int {
			if r.setuidDone && r.euidAfter != nil {
				return *r.euidAfter
			}
			return r.effUID
		},
		setgid: func(gid int) error {
			r.calls = append(r.calls, fmt.Sprintf("setgid(%d)", gid))
			if r.setgidErr != nil {
				return r.setgidErr
			}
			r.realGID, r.effGID = gid, gid
			return nil
		},
		initgroups: func(gids []uint32, gmuid uint32) error {
			r.gids = append([]uint32(nil), gids...)
			r.gmuid = gmuid
			r.calls = append(r.calls, fmt.Sprintf("initgroups(%v, %d)", gids, gmuid))
			if r.initgroupsErr != nil {
				return r.initgroupsErr
			}
			r.suppGroups = []int{int(gids[0])}
			return nil
		},
		setuid: func(uid int) error {
			r.calls = append(r.calls, fmt.Sprintf("setuid(%d)", uid))
			if r.setuidErr != nil {
				return r.setuidErr
			}
			// A process that dropped root cannot setuid back to 0 unless it kept
			// root in a saved uid, which regainRoot models.
			if uid == 0 && r.effUID != 0 && !r.regainRoot {
				return syscall.EPERM
			}
			r.realUID, r.effUID = uid, uid
			r.setuidDone = true
			return nil
		},
		getuid: func() int {
			if r.uidAfter != nil {
				return *r.uidAfter
			}
			return r.realUID
		},
		getgid: func() int {
			if r.gidAfter != nil {
				return *r.gidAfter
			}
			return r.realGID
		},
		getegid: func() int {
			if r.egidAfter != nil {
				return *r.egidAfter
			}
			return r.effGID
		},
		getgroups: func() ([]int, error) {
			r.calls = append(r.calls, "getgroups")
			if r.getgroupsErr != nil {
				return nil, r.getgroupsErr
			}
			if r.groupsAfter != nil {
				return append([]int(nil), r.groupsAfter...), nil
			}
			return append([]int(nil), r.suppGroups...), nil
		},
	}
}

func setTestIdentity(t *testing.T, uid, gid uint32) {
	t.Helper()
	t.Setenv(accountHelperUIDEnv, fmt.Sprint(uid))
	t.Setenv(accountHelperGIDEnv, fmt.Sprint(gid))
}

// unsetTestIdentity removes the identity variables for one test and restores
// whatever the process had afterwards.
func unsetTestIdentity(t *testing.T) {
	t.Helper()
	for _, key := range []string{accountHelperUIDEnv, accountHelperGIDEnv} {
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

// TestEnterHelperAccountSwitchesInOrder pins the sequence: the real, effective
// and saved gid first, then the groups with the account uid as gmuid, then the
// real, effective and saved uid. Only the primary group may reach the static
// initgroups list.
func TestEnterHelperAccountSwitchesInOrder(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()

	if err := enterHelperAccountFromEnv(r.ops()); err != nil {
		t.Fatalf("enterHelperAccountFromEnv: %v", err)
	}

	want := []string{"setgid(20)", "initgroups([20], 501)", "setuid(501)", "setuid(0)", "getgroups"}
	if !slices.Equal(r.calls, want) {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}
	if wantGids := []uint32{20}; !slices.Equal(r.gids, wantGids) {
		t.Errorf("initgroups gids = %v, want %v", r.gids, wantGids)
	}
	if r.gmuid != 501 {
		t.Errorf("initgroups gmuid = %d, want 501", r.gmuid)
	}
}

// TestEnterHelperAccountLargeGroupListNeverReachesSetgroups is the regression
// assertion at the switch level: the account may belong to more groups than
// Darwin's NGROUPS, but the switch hands the kernel only the primary group and
// performs no setgroups call at all.
func TestEnterHelperAccountLargeGroupListNeverReachesSetgroups(t *testing.T) {
	setTestIdentity(t, 502, 20)
	r := newSwitchRecorder()
	// The kernel resolves the account's groups through initgroups, so the
	// resulting table may hold more entries than NGROUPS. None of it may reach a
	// setgroups call.
	r.groupsAfter = []int{20}
	for i := 0; i < 17; i++ {
		r.groupsAfter = append(r.groupsAfter, 200+i)
	}

	if err := enterHelperAccountFromEnv(r.ops()); err != nil {
		t.Fatalf("enterHelperAccountFromEnv: %v", err)
	}

	want := []string{"setgid(20)", "initgroups([20], 502)", "setuid(502)", "setuid(0)", "getgroups"}
	if !slices.Equal(r.calls, want) {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}
	if wantGids := []uint32{20}; !slices.Equal(r.gids, wantGids) {
		t.Errorf("initgroups gids = %v, want %v: the full group table was passed", r.gids, wantGids)
	}
}

// TestEnterHelperAccountRefusesNonRoot stops an ordinary account from naming a
// different identity in the environment.
func TestEnterHelperAccountRefusesNonRoot(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()
	r.effUID = 1001

	err := enterHelperAccountFromEnv(r.ops())
	if err == nil {
		t.Fatal("enterHelperAccountFromEnv succeeded without root")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error = %q, want it to name the missing root privilege", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("calls = %v, want none", r.calls)
	}
}

// TestEnterHelperAccountWithoutEnvIsNoop covers Linux children, which are
// already the account when they start, and in-process callers.
func TestEnterHelperAccountWithoutEnvIsNoop(t *testing.T) {
	unsetTestIdentity(t)
	ops := helperIdentityOps{
		geteuid:    func() int { t.Error("geteuid called without an identity in the environment"); return 0 },
		setgid:     func(int) error { t.Error("setgid called without an identity"); return nil },
		initgroups: func([]uint32, uint32) error { t.Error("initgroups called without an identity"); return nil },
		setuid:     func(int) error { t.Error("setuid called without an identity"); return nil },
		getuid:     func() int { t.Error("getuid called without an identity"); return 0 },
		getgid:     func() int { t.Error("getgid called without an identity"); return 0 },
		getegid:    func() int { t.Error("getegid called without an identity"); return 0 },
	}
	if err := enterHelperAccountFromEnv(ops); err != nil {
		t.Fatalf("enterHelperAccountFromEnv without env: %v", err)
	}
}

// TestEnterHelperAccountChecksEveryIdentityHalf fails the switch when any of
// the four ids does not match, so a half-switched process never serves.
func TestEnterHelperAccountChecksEveryIdentityHalf(t *testing.T) {
	const uid, gid = 501, 20
	wrongUID, wrongGID := 0, 99
	cases := []struct {
		name string
		edit func(*switchRecorder)
		want string
	}{
		{"real uid", func(r *switchRecorder) { r.uidAfter = &wrongUID }, "real uid"},
		{"effective uid", func(r *switchRecorder) { r.euidAfter = &wrongUID }, "effective uid"},
		{"real gid", func(r *switchRecorder) { r.gidAfter = &wrongGID }, "real gid"},
		{"effective gid", func(r *switchRecorder) { r.egidAfter = &wrongGID }, "effective gid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setTestIdentity(t, uid, gid)
			r := newSwitchRecorder()
			tc.edit(r)

			err := enterHelperAccountFromEnv(r.ops())
			if err == nil {
				t.Fatalf("enterHelperAccountFromEnv succeeded with a wrong %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err, tc.want)
			}
			if len(r.calls) != 3 {
				t.Errorf("calls = %v, want the three switch steps", r.calls)
			}
		})
	}
}

// TestEnterHelperAccountRejectsRegainingRoot is the post-drop check OpenSSH's
// permanently_set_uid makes: if setuid(0) succeeds after the switch, the
// process still holds root somewhere and must not serve.
func TestEnterHelperAccountRejectsRegainingRoot(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()
	r.regainRoot = true

	err := enterHelperAccountFromEnv(r.ops())
	if err == nil {
		t.Fatal("enterHelperAccountFromEnv succeeded even though setuid(0) worked after the switch")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error = %q, want it to name the regained root identity", err)
	}
}

// TestEnterHelperAccountRejectsRootSupplementaryGroup stops a process that
// still belongs to gid 0 after the switch: that group grants access the account
// must not keep.
func TestEnterHelperAccountRejectsRootSupplementaryGroup(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()
	r.groupsAfter = []int{20, 0}

	err := enterHelperAccountFromEnv(r.ops())
	if err == nil {
		t.Fatal("enterHelperAccountFromEnv succeeded with gid 0 in the supplementary groups")
	}
	if !strings.Contains(err.Error(), "gid 0") {
		t.Errorf("error = %q, want it to name the root group", err)
	}
}

// TestEnterHelperAccountAllowsRootPrimaryGroup skips the gid 0 assertion when
// the account's own primary group is 0: the group table legitimately contains
// it and the switch is otherwise fine.
func TestEnterHelperAccountAllowsRootPrimaryGroup(t *testing.T) {
	setTestIdentity(t, 501, 0)
	r := newSwitchRecorder()
	r.groupsAfter = []int{0, 20}

	if err := enterHelperAccountFromEnv(r.ops()); err != nil {
		t.Fatalf("enterHelperAccountFromEnv with primary gid 0: %v", err)
	}
}

// TestEnterHelperAccountRejectsZeroTargetUID refuses a target that would keep
// the helper as root, whatever the environment says.
func TestEnterHelperAccountRejectsZeroTargetUID(t *testing.T) {
	setTestIdentity(t, 0, 20)
	r := newSwitchRecorder()

	err := enterHelperAccountFromEnv(r.ops())
	if err == nil {
		t.Fatal("enterHelperAccountFromEnv accepted a switch to uid 0")
	}
	if !strings.Contains(err.Error(), "uid 0") {
		t.Errorf("error = %q, want it to name the root uid", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("calls = %v, want none", r.calls)
	}
}

// TestEnterHelperAccountFailsWhenGroupsUnreadable fails closed when the group
// table cannot be read after the switch: without it the gid 0 check cannot run.
func TestEnterHelperAccountFailsWhenGroupsUnreadable(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()
	r.getgroupsErr = syscall.EIO

	err := enterHelperAccountFromEnv(r.ops())
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("error = %v, want %v", err, syscall.EIO)
	}
}

// TestEnterHelperAccountStopsOnSwitchFailure closes the switch on the first
// error and never drops privileges after a failure.
func TestEnterHelperAccountStopsOnSwitchFailure(t *testing.T) {
	const uid, gid = 501, 20
	cases := []struct {
		name  string
		edit  func(*switchRecorder)
		want  []string
		errno error
	}{
		{
			name:  "setgid",
			edit:  func(r *switchRecorder) { r.setgidErr = syscall.EPERM },
			want:  []string{"setgid(20)"},
			errno: syscall.EPERM,
		},
		{
			name:  "initgroups",
			edit:  func(r *switchRecorder) { r.initgroupsErr = syscall.EINVAL },
			want:  []string{"setgid(20)", "initgroups([20], 501)"},
			errno: syscall.EINVAL,
		},
		{
			name:  "setuid",
			edit:  func(r *switchRecorder) { r.setuidErr = syscall.EPERM },
			want:  []string{"setgid(20)", "initgroups([20], 501)", "setuid(501)"},
			errno: syscall.EPERM,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setTestIdentity(t, uid, gid)
			r := newSwitchRecorder()
			tc.edit(r)

			err := enterHelperAccountFromEnv(r.ops())
			if !errors.Is(err, tc.errno) {
				t.Fatalf("error = %v, want %v", err, tc.errno)
			}
			if !slices.Equal(r.calls, tc.want) {
				t.Errorf("calls = %v, want %v", r.calls, tc.want)
			}
		})
	}
}

// TestEnterHelperAccountRejectsBadEnvironment reports a malformed identity
// instead of switching to a partial one.
func TestEnterHelperAccountRejectsBadEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  func(t *testing.T)
	}{
		{"uid not a number", func(t *testing.T) {
			t.Setenv(accountHelperUIDEnv, "alice")
			t.Setenv(accountHelperGIDEnv, "20")
		}},
		{"gid missing", func(t *testing.T) {
			t.Setenv(accountHelperUIDEnv, "501")
			unsetOne(t, accountHelperGIDEnv)
		}},
		{"gid not a number", func(t *testing.T) {
			t.Setenv(accountHelperUIDEnv, "501")
			t.Setenv(accountHelperGIDEnv, "wheel")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.env(t)
			r := newSwitchRecorder()
			err := enterHelperAccountFromEnv(r.ops())
			if err == nil {
				t.Fatal("enterHelperAccountFromEnv accepted a bad environment")
			}
			if len(r.calls) != 0 {
				t.Errorf("calls = %v, want none", r.calls)
			}
		})
	}
}

// unsetOne removes one variable for the rest of the test and restores it in
// cleanup.
func unsetOne(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		}
	})
}

func TestHelperIdentityEnv(t *testing.T) {
	got := helperIdentityEnv(Account{UID: 501, GID: 20})
	want := []string{accountHelperUIDEnv + "=501", accountHelperGIDEnv + "=20"}
	if !slices.Equal(got, want) {
		t.Errorf("helperIdentityEnv = %q, want %q", got, want)
	}
}

// TestEnterHelperAccountClearsIdentityEnvironment checks that the consumed
// identity does not stay in the process environment.
func TestEnterHelperAccountClearsIdentityEnvironment(t *testing.T) {
	setTestIdentity(t, 501, 20)
	r := newSwitchRecorder()

	if err := enterHelperAccountFromEnv(r.ops()); err != nil {
		t.Fatalf("enterHelperAccountFromEnv: %v", err)
	}
	for _, key := range []string{accountHelperUIDEnv, accountHelperGIDEnv} {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("%s is still set after the switch", key)
		}
	}
}
