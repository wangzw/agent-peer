package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wangzw/agent-peer/internal/broker/helperpb"
)

// The access-check process execs as root and receives the identity it must
// check as in its environment. These are not part of the wire protocol: the
// switch has to happen before the child reads any caller-controlled byte, and
// the Broker constructs the environment itself.
const (
	accessCheckUIDEnv    = "AGENT_PEER_ACCESS_CHECK_UID"
	accessCheckGIDEnv    = "AGENT_PEER_ACCESS_CHECK_GID"
	accessCheckGroupsEnv = "AGENT_PEER_ACCESS_CHECK_GROUPS"
)

// Access is the permission a caller asks for when reading or writing the
// Broker configuration file.
type Access uint8

const (
	AccessRead Access = iota + 1
	AccessWrite
)

// defaultAccessCheckTimeout bounds one permission check from start to result.
const defaultAccessCheckTimeout = 5 * time.Second

// RunAccessCheck is the body of `agent-peer internal access-check`: it first
// drops its effective identity to the account named by the Broker, then reads
// one AccessCheckRequest from stdin, checks it as that account, writes one
// AccessCheckResponse to stdout and returns.
func RunAccessCheck(stdin io.Reader, stdout io.Writer) error {
	if err := enterAccountFromEnv(); err != nil {
		// Report the failure in the framed response before exiting. The
		// process's stderr is discarded by the Broker, so the response is the
		// only path for the errno to reach the Broker log. The response is
		// never allowed: it carries the error and the process exits with a
		// failure.
		resp := &helperpb.AccessCheckResponse{
			ProtocolVersion: helperpb.ProtocolVersion,
			Uid:             uint32(os.Geteuid()),
			Allowed:         false,
			Error:           err.Error(),
		}
		if writeErr := newMsgWriter(stdout).write(resp); writeErr != nil {
			return fmt.Errorf("access-check: %w (reporting it: %v)", err, writeErr)
		}
		return fmt.Errorf("access-check: %w", err)
	}

	var req helperpb.AccessCheckRequest
	if err := newMsgReader(stdin).read(&req); err != nil {
		return fmt.Errorf("access-check: reading request: %w", err)
	}

	resp := &helperpb.AccessCheckResponse{
		ProtocolVersion: helperpb.ProtocolVersion,
		// The check runs as the effective identity, which is the account the
		// Broker asked about; the real and saved uid stay root.
		Uid:     uint32(os.Geteuid()),
		Allowed: false,
	}
	switch {
	case req.GetProtocolVersion() != helperpb.ProtocolVersion:
		resp.Error = fmt.Sprintf("unsupported protocol version %d", req.GetProtocolVersion())
	case req.GetAccess() == helperpb.Access_ACCESS_UNSPECIFIED:
		resp.Error = "unspecified access"
	default:
		mode := uint32(unix.R_OK)
		if req.GetAccess() == helperpb.Access_ACCESS_WRITE {
			mode = uint32(unix.W_OK)
		}
		// The process switched its effective identity above, and faccessat2
		// judges that identity while still applying the ACL. A kernel that does
		// not implement faccessat2 must surface as a failed check: falling back
		// to the permission bits would ignore the ACL.
		switch err := accessCheckSyscall(req.GetPath(), mode); {
		case err == nil:
			resp.Allowed = true
		case isAccessDenial(err):
			// The account lacks the requested permission. That is a result of
			// the check, not a failure of it.
		default:
			resp.Error = err.Error()
		}
	}

	if err := newMsgWriter(stdout).write(resp); err != nil {
		return fmt.Errorf("access-check: writing response: %w", err)
	}
	return nil
}

// accessCheckSyscall performs the access check on path for mode as the
// process's effective identity. On Linux it calls faccessat2 directly, which
// applies the effective identity and the ACL and never falls back to the
// permission bits. Tests replace it to simulate a kernel without faccessat2.
var accessCheckSyscall = faccessat2

// isAccessDenial reports whether err is the access check saying "no" rather
// than the check itself failing. EPERM and ENOSYS are deliberately absent: a
// kernel or sandbox that refuses faccessat2 must be reported as a failed check,
// not as a denial judged from the permission bits.
func isAccessDenial(err error) bool {
	return errors.Is(err, unix.EACCES) ||
		errors.Is(err, unix.ENOENT) ||
		errors.Is(err, unix.ENOTDIR) ||
		errors.Is(err, unix.ELOOP) ||
		errors.Is(err, unix.EROFS)
}

// enterAccountFromEnv switches the process to the account named by the Broker.
// Without the identity variables the process is already the account: that is
// how in-process unit tests call RunAccessCheck. Membership is replaced, not
// extended, and only the effective ids change, so the real and saved uid stay
// root and the account cannot signal or inspect the process.
func enterAccountFromEnv() error {
	uidValue, ok := os.LookupEnv(accessCheckUIDEnv)
	if !ok {
		return nil
	}
	uid, err := parseID(uidValue)
	if err != nil {
		return fmt.Errorf("account uid %q: %w", uidValue, err)
	}

	gidValue, ok := os.LookupEnv(accessCheckGIDEnv)
	if !ok {
		return errors.New("account gid is missing")
	}
	gid, err := parseID(gidValue)
	if err != nil {
		return fmt.Errorf("account gid %q: %w", gidValue, err)
	}

	groups, err := parseIDList(os.Getenv(accessCheckGroupsEnv))
	if err != nil {
		return fmt.Errorf("account groups: %w", err)
	}
	return becomeAccount(uid, gid, groups)
}

// parseIDList parses the comma-separated group ids the Broker passes in the
// environment. An empty value is an empty list.
func parseIDList(value string) ([]uint32, error) {
	if value == "" {
		return nil, nil
	}
	fields := strings.Split(value, ",")
	ids := make([]uint32, 0, len(fields))
	for _, field := range fields {
		id, err := parseID(field)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", field, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// becomeAccount makes the process check as uid. A check whose target identity
// is already the process's effective identity needs no switch; this also
// covers the root access check on macOS, where the account database can list
// more groups than Darwin's NGROUPS limit. Otherwise the switch is
// platform-specific: Linux replaces the supplementary groups, while Darwin
// lets the kernel resolve them through memberd. The calls require the process
// to be root, which the Broker arranges by execing it without a credential
// change. The real and saved uid are deliberately left alone.
func becomeAccount(uid, gid uint32, groups []uint32) error {
	if uint32(os.Geteuid()) == uid && uint32(os.Getegid()) == gid {
		return nil
	}
	return switchAccount(uid, gid, groups)
}

// AccessChecker decides configuration file permissions by running one
// access-check process for the calling account. The process execs as root and
// checks with only its effective identity set to the account, so the account
// never owns it. Results are never cached.
type AccessChecker struct {
	Exe     string                                                    // required unless Start is set
	Lookup  func(uid uint32) (Account, error)                         // default LookupAccount
	Timeout time.Duration                                             // default 5s
	Start   func(ctx context.Context, acct Account) (*Process, error) // default: accountCommand(Exe, []string{"internal", "access-check"}, acct) with pipes
	Logger  *slog.Logger
}

// Check reports whether the account uid may access path. It spawns one process per call.
func (c *AccessChecker) Check(ctx context.Context, uid uint32, path string, need Access) (bool, error) {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}

	lookup := c.Lookup
	if lookup == nil {
		lookup = LookupAccount
	}
	acct, err := lookup(uid)
	if err != nil {
		if errors.Is(err, ErrNoAccount) {
			log.Warn("access_check_unknown_account", "uid", uid)
			return false, nil
		}
		return false, fmt.Errorf("helper: access check for uid %d: %w", uid, err)
	}

	start := c.Start
	if start == nil {
		if c.Exe == "" {
			return false, errors.New("helper: access check: no executable configured")
		}
		start = accessCheckStart(c.Exe)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultAccessCheckTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc, err := start(ctx, acct)
	if err != nil {
		return false, fmt.Errorf("helper: access check for uid %d: starting process: %w", uid, err)
	}

	req := &helperpb.AccessCheckRequest{
		ProtocolVersion: helperpb.ProtocolVersion,
		Path:            path,
	}
	switch need {
	case AccessRead:
		req.Access = helperpb.Access_ACCESS_READ
	case AccessWrite:
		req.Access = helperpb.Access_ACCESS_WRITE
	}
	if err := newMsgWriter(proc.Stdin).write(req); err != nil {
		stopProcess(proc)
		return false, fmt.Errorf("helper: access check for uid %d: sending request: %w", uid, err)
	}
	// The child reads exactly one request; closing stdin tells it no more
	// requests follow. A close error will surface as a read error below.
	_ = proc.Stdin.Close()

	type response struct {
		msg *helperpb.AccessCheckResponse
		err error
	}
	// The read blocks on a pipe, which a context cannot interrupt, so it runs
	// in its own goroutine and the select below abandons it on timeout. The
	// channel is buffered so the goroutine can finish after Check returns.
	read := make(chan response, 1)
	go func() {
		var msg helperpb.AccessCheckResponse
		err := newMsgReader(proc.Stdout).read(&msg)
		read <- response{msg: &msg, err: err}
	}()

	var res response
	select {
	case res = <-read:
	case <-ctx.Done():
		stopProcess(proc)
		return false, fmt.Errorf("helper: access check for uid %d: waiting for the check process: %w", uid, ctx.Err())
	}
	if res.err != nil {
		stopProcess(proc)
		return false, fmt.Errorf("helper: access check for uid %d: reading result: %w", uid, res.err)
	}

	// The child exits after writing its result. Reap it before returning so no
	// zombie is left behind, but report a bad answer ahead of a wait error: the
	// answer explains what went wrong.
	waitErr := proc.Wait()
	if got := res.msg.GetProtocolVersion(); got != helperpb.ProtocolVersion {
		return false, fmt.Errorf("helper: access check for uid %d: check process reported protocol version %d", uid, got)
	}
	// Check the reported error before the uid: a failed identity switch is
	// reported with the process's current effective uid, and that errno must
	// reach the Broker log instead of being replaced by a uid mismatch. An
	// error response is never allowed, whatever its Allowed field says.
	if msg := res.msg.GetError(); msg != "" {
		log.Warn("access_check_failed", "uid", uid, "error", msg)
		return false, fmt.Errorf("helper: access check for uid %d: %s", uid, msg)
	}
	if got := res.msg.GetUid(); got != uid {
		return false, fmt.Errorf("helper: access check for uid %d: check process ran as uid %d", uid, got)
	}
	if waitErr != nil {
		return false, fmt.Errorf("helper: access check for uid %d: check process failed: %w", uid, waitErr)
	}
	return res.msg.GetAllowed(), nil
}

// accessCheckStart returns the default Start function for AccessChecker: it
// runs the access-check subcommand of exe as root with the account identity,
// and the subcommand drops only its effective identity.
func accessCheckStart(exe string) func(context.Context, Account) (*Process, error) {
	return func(_ context.Context, acct Account) (*Process, error) {
		cmd := accessCheckCommand(exe, acct)
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
		return &Process{
			Stdin:  stdin,
			Stdout: stdout,
			Wait:   cmd.Wait,
			Kill:   cmd.Process.Kill,
		}, nil
	}
}

// stopProcess kills and reaps proc, ignoring failures on either step.
func stopProcess(proc *Process) {
	if proc.Kill != nil {
		_ = proc.Kill()
	}
	if proc.Wait != nil {
		_ = proc.Wait()
	}
}
