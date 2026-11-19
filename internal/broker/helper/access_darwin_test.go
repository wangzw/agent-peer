//go:build darwin

package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/wangzw/agent-peer/internal/broker/helperpb"
)

// switchErrRole is a test-only child role whose identity switch fails with
// EINVAL. TestCheckLogsSwitchFailure uses it to exercise the response path
// without a real credential change.
const switchErrRole = "access-check-switch-error"

func init() {
	testRoleHooks[switchErrRole] = func() int {
		// Drain the request first so the parent's write cannot race with this
		// process exiting; the switch below is the behavior under test.
		_, _ = io.Copy(io.Discard, os.Stdin)
		switchSetegid = func(int) error { return nil }
		switchInitgroups = func([]uint32, uint32) error { return syscall.EINVAL }
		switchSeteuid = func(int) error { return nil }
		if err := RunAccessCheck(os.Stdin, os.Stdout); err != nil {
			return 1
		}
		return 0
	}
}

// stubDarwinSwitch replaces the three Darwin syscall seams for one test.
func stubDarwinSwitch(t *testing.T, setegid func(int) error, initgroups func([]uint32, uint32) error, seteuid func(int) error) {
	t.Helper()
	oldSetegid, oldInitgroups, oldSeteuid := switchSetegid, switchInitgroups, switchSeteuid
	t.Cleanup(func() {
		switchSetegid, switchInitgroups, switchSeteuid = oldSetegid, oldInitgroups, oldSeteuid
	})
	switchSetegid = setegid
	switchInitgroups = initgroups
	switchSeteuid = seteuid
}

// testExecStartEnv runs the current test binary in role with extra environment
// variables. The child runs as the test process, with no credential change.
func testExecStartEnv(t *testing.T, role string, env []string) func(context.Context, Account) (*Process, error) {
	t.Helper()
	home := t.TempDir()
	path := os.Getenv("PATH")
	return func(context.Context, Account) (*Process, error) {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append([]string{
			"AGENT_PEER_TEST_HELPER_ROLE=" + role,
			"HOME=" + home,
			"PATH=" + path,
		}, env...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return &Process{
			Stdin:  stdin,
			Stdout: stdout,
			Wait:   cmd.Wait,
			Kill:   cmd.Process.Kill,
		}, nil
	}
}

// TestBecomeAccountDarwinUsesInitgroupsWithGMUID covers the switch for an
// account whose group list exceeds Darwin's NGROUPS. The account's list is
// ignored: only the primary group goes into the static list, and the account
// uid goes in as gmuid so the kernel resolves the rest through memberd.
func TestBecomeAccountDarwinUsesInitgroupsWithGMUID(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000

	var calls []string
	var gotGids []uint32
	stubDarwinSwitch(t,
		func(got int) error {
			calls = append(calls, fmt.Sprintf("setegid(%d)", got))
			return nil
		},
		func(gids []uint32, gmuid uint32) error {
			gotGids = append([]uint32(nil), gids...)
			calls = append(calls, fmt.Sprintf("initgroups(%v, %d)", gids, gmuid))
			return nil
		},
		func(got int) error {
			calls = append(calls, fmt.Sprintf("seteuid(%d)", got))
			return nil
		},
	)

	// More groups than NGROUPS: the full list must not reach setgroups or the
	// initgroups static argument.
	groups := make([]uint32, 64)
	for i := range groups {
		groups[i] = gid + uint32(i)
	}
	if err := becomeAccount(uid, gid, groups); err != nil {
		t.Fatalf("becomeAccount with %d groups: %v", len(groups), err)
	}

	if want := []uint32{gid}; !slices.Equal(gotGids, want) {
		t.Errorf("initgroups gids = %v, want %v: the account's full list was passed", gotGids, want)
	}
	want := []string{
		fmt.Sprintf("setegid(%d)", gid),
		fmt.Sprintf("initgroups([%d], %d)", gid, uid),
		fmt.Sprintf("seteuid(%d)", uid),
	}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// TestBecomeAccountDarwinSetegidFailureStops covers the first step: an EPERM
// from setegid returns before initgroups and seteuid run.
func TestBecomeAccountDarwinSetegidFailureStops(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000

	var calls []string
	stubDarwinSwitch(t,
		func(int) error {
			calls = append(calls, "setegid")
			return syscall.EPERM
		},
		func([]uint32, uint32) error {
			calls = append(calls, "initgroups")
			return nil
		},
		func(int) error {
			calls = append(calls, "seteuid")
			return nil
		},
	)

	err := becomeAccount(uid, gid, nil)
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("error = %v, want syscall.EPERM", err)
	}
	if want := []string{"setegid"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// TestBecomeAccountDarwinInitgroupsFailureStops closes the switch on the first
// error: the errno is returned to RunAccessCheck, which reports it in the
// response for the Broker log, and the effective uid is never dropped.
func TestBecomeAccountDarwinInitgroupsFailureStops(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000

	var calls []string
	stubDarwinSwitch(t,
		func(int) error {
			calls = append(calls, "setegid")
			return nil
		},
		func([]uint32, uint32) error {
			calls = append(calls, "initgroups")
			return syscall.EINVAL
		},
		func(int) error {
			calls = append(calls, "seteuid")
			return nil
		},
	)

	err := becomeAccount(uid, gid, nil)
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("error = %v, want syscall.EINVAL", err)
	}
	if !strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("error %q does not carry the errno text", err)
	}
	if want := []string{"setegid", "initgroups"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// TestBecomeAccountDarwinSeteuidFailureIsReported covers the last step.
func TestBecomeAccountDarwinSeteuidFailureIsReported(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000

	stubDarwinSwitch(t,
		func(int) error { return nil },
		func([]uint32, uint32) error { return nil },
		func(int) error { return syscall.EPERM },
	)
	if err := becomeAccount(uid, gid, nil); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("error = %v, want syscall.EPERM", err)
	}
}

// TestBecomeAccountDarwinSkipsCurrentIdentity keeps the rule that a check for
// the current effective identity does not touch the groups.
func TestBecomeAccountDarwinSkipsCurrentIdentity(t *testing.T) {
	called := false
	stubDarwinSwitch(t,
		func(int) error { called = true; return nil },
		func([]uint32, uint32) error { called = true; return nil },
		func(int) error { called = true; return nil },
	)
	if err := becomeAccount(uint32(os.Geteuid()), uint32(os.Getegid()), nil); err != nil {
		t.Fatalf("becomeAccount for the current identity: %v", err)
	}
	if called {
		t.Error("switchAccount ran for the current identity")
	}
}

// TestRunAccessCheckSwitchFailureWritesErrorResponse checks the response path:
// a failed switch writes the errno into the response and still reports a
// failure.
func TestRunAccessCheckSwitchFailureWritesErrorResponse(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000
	t.Setenv(accessCheckUIDEnv, strconv.FormatUint(uint64(uid), 10))
	t.Setenv(accessCheckGIDEnv, strconv.FormatUint(uint64(gid), 10))
	t.Setenv(accessCheckGroupsEnv, strconv.FormatUint(uint64(gid), 10))
	stubDarwinSwitch(t,
		func(int) error { return nil },
		func([]uint32, uint32) error { return syscall.EINVAL },
		func(int) error { return nil },
	)

	var out bytes.Buffer
	err := RunAccessCheck(bytes.NewReader(nil), &out)
	if err == nil {
		t.Fatal("RunAccessCheck returned nil, want the switch error")
	}
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("error = %v, want syscall.EINVAL", err)
	}
	if !strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("error %q does not carry the errno text", err)
	}

	var resp helperpb.AccessCheckResponse
	if uerr := (protodelim.UnmarshalOptions{MaxSize: 4 << 20}).UnmarshalFrom(bufio.NewReader(&out), &resp); uerr != nil {
		t.Fatalf("decoding the response: %v", uerr)
	}
	if !strings.Contains(resp.GetError(), "invalid argument") {
		t.Errorf("response error = %q, want it to carry the errno text", resp.GetError())
	}
	if resp.GetAllowed() {
		t.Error("allowed = true in a failed switch response")
	}
	if got, want := resp.GetUid(), uint32(os.Geteuid()); got != want {
		t.Errorf("response uid = %d, want the current effective uid %d", got, want)
	}
}

// TestCheckLogsSwitchFailure runs the real helper as a child whose switch
// fails with EINVAL. The parent must return an error and log the errno text.
func TestCheckLogsSwitchFailure(t *testing.T) {
	uid := uint32(os.Geteuid()) + 1000
	gid := uint32(os.Getegid()) + 1000
	env := []string{
		accessCheckUIDEnv + "=" + strconv.FormatUint(uint64(uid), 10),
		accessCheckGIDEnv + "=" + strconv.FormatUint(uint64(gid), 10),
		accessCheckGroupsEnv + "=" + strconv.FormatUint(uint64(gid), 10),
	}

	var logBuf bytes.Buffer
	checker := &AccessChecker{
		Lookup: func(uint32) (Account, error) {
			return Account{UID: uid, GID: gid, Groups: []uint32{gid}}, nil
		},
		Start:   testExecStartEnv(t, switchErrRole, env),
		Timeout: 5 * time.Second,
		Logger:  slog.New(slog.NewTextHandler(&logBuf, nil)),
	}

	allowed, err := checker.Check(context.Background(), uid, "/config.yaml", AccessRead)
	if err == nil {
		t.Fatalf("Check = (%v, nil), want the switch error", allowed)
	}
	if allowed {
		t.Error("allowed = true when the switch failed")
	}
	if !strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("Check error = %q, want the errno text", err)
	}
	if !strings.Contains(logBuf.String(), "invalid argument") {
		t.Errorf("log %q does not record the errno text", logBuf.String())
	}
}
