package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// statusProbeTimeout bounds one Hello and Status exchange on the local socket.
// The probe runs after the service is ready, so the Broker is already
// listening and should answer promptly.
const statusProbeTimeout = 2 * time.Second

// DegradedLevel is the wire enum the degraded state carries.
type DegradedLevel = localpb.DegradedLevel

// The degraded levels a Status response can carry.
const (
	DegradedLocalOnly       = localpb.DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY
	DegradedDiagnosticsOnly = localpb.DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY
)

// DegradedState is the Status a degraded Broker reports, as the service
// commands print it.
type DegradedState struct {
	Level  localpb.DegradedLevel
	Step   string
	Reason string
	Fix    string
}

// DegradedError reports that the service is installed and running but the
// Broker runs degraded. Install, and the start and restart commands, return it
// together with a complete Result; the service is left running. The command
// prints State and exits with exitServiceDegraded.
type DegradedError struct {
	State DegradedState
}

// Error names the level and the reason so an error logged without the printed
// summary still says why the Broker runs degraded.
func (e *DegradedError) Error() string {
	msg := "the Broker is running degraded"
	if e.State.Level != localpb.DegradedLevel_DEGRADED_LEVEL_UNSPECIFIED {
		msg += " (" + localpb.DegradedLevelLabel(e.State.Level) + ")"
	}
	if e.State.Reason != "" {
		msg += ": " + e.State.Reason
	}
	return msg
}

// defaultStatusProbe asks the Broker on socketPath for its status and reports
// the degraded state it carries. An error means no answer was received, or the
// socket is served by an account that may not run this level's Broker.
func defaultStatusProbe(socketPath string) (DegradedState, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeTimeout)
	defer cancel()

	abs, err := filepath.Abs(socketPath)
	if err != nil {
		return DegradedState{}, false, err
	}
	conn, err := grpc.NewClient("unix://"+abs,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialStatusSocket),
	)
	if err != nil {
		return DegradedState{}, false, err
	}
	defer func() { _ = conn.Close() }()

	client := localpb.NewLocalClient(conn)
	if _, err := client.Hello(ctx, &localpb.HelloRequest{ProtocolVersion: localpb.ProtocolVersion}); err != nil {
		return DegradedState{}, false, err
	}
	resp, err := client.Status(ctx, &localpb.StatusRequest{})
	if err != nil {
		return DegradedState{}, false, err
	}
	degraded := resp.GetDegraded()
	if degraded == nil {
		return DegradedState{}, false, nil
	}
	return DegradedState{
		Level:  degraded.GetLevel(),
		Step:   degraded.GetStep(),
		Reason: degraded.GetReason(),
		Fix:    degraded.GetFix(),
	}, true, nil
}

// dialStatusSocket dials the local Broker socket, reads the peer's account
// before gRPC sends a byte, and rejects an account that may not serve this
// level's Broker. A socket taken over by another account is therefore never
// asked for its status.
func dialStatusSocket(ctx context.Context, address string) (net.Conn, error) {
	path := socketPathFromAddress(address)
	d := net.Dialer{Timeout: probeTimeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	uid, err := statusPeerUID(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cannot read the peer credentials of %s: %w", path, err)
	}
	if !statusPeerAllowed(path, uid) {
		_ = conn.Close()
		return nil, fmt.Errorf("the Broker socket %s is served by uid %d, not %s", path, uid, statusPeerWant(path))
	}
	return conn, nil
}

// socketPathFromAddress turns the target gRPC hands to a context dialer back
// into a socket path.
func socketPathFromAddress(address string) string {
	if path, ok := strings.CutPrefix(address, "unix://"); ok {
		return path
	}
	if path, ok := strings.CutPrefix(address, "unix:"); ok {
		return path
	}
	return address
}

// statusPeerAllowed reports whether uid may answer the status probe on the
// socket at path. A system socket is served by root; a user one by
// this account.
func statusPeerAllowed(path string, uid uint32) bool {
	if isSystemSocket(path) {
		return uid == 0
	}
	return uid == uint32(os.Geteuid())
}

// statusPeerWant names the account statusPeerAllowed expects, for errors.
func statusPeerWant(path string) string {
	if isSystemSocket(path) {
		return "root"
	}
	return fmt.Sprintf("this account (%d)", os.Geteuid())
}

// isSystemSocket reports whether path is the local socket a system
// Broker binds on either supported platform.
func isSystemSocket(path string) bool {
	path = filepath.Clean(path)
	for _, goos := range []string{"linux", "darwin"} {
		if path == localSocketPath(Options{System: true, GOOS: goos}) {
			return true
		}
	}
	return false
}
