package broker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/inbox"
)

// Broker serves the local interface and holds the agent table. Handler methods
// live in the files of the work item that owns the RPC; this file owns the
// structure, the gRPC server and the process-lifetime context.
type Broker struct {
	localpb.UnimplementedLocalServer
	deps   Deps
	log    *slog.Logger
	grpc   *grpc.Server
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	// agent table fields: added by the registration work item
	// downstream state
}

// New assembles the Broker. Deps.Topics and Deps.Config are required; the
// other nil fields take the defaults documented on Deps. The gRPC server
// options are assembled in a fixed order: the peer credentials, then
// versionServerOptions, then Deps.ServerOptions. Appending the version
// interceptors before the caller's ones is what makes a rejected connection
// get version_mismatch before the access interceptor runs.
func New(deps Deps) (*Broker, error) {
	if deps.Topics == nil {
		return nil, fmt.Errorf("broker: Deps.Topics is required")
	}
	if deps.Config == nil {
		return nil, fmt.Errorf("broker: Deps.Config is required")
	}
	if deps.Sessions == nil {
		deps.Sessions = UserRouter()
	}
	if deps.Clock == nil {
		deps.Clock = SystemClock()
	}
	if deps.Logger == nil {
		// The zero LevelVar means info.
		deps.Logger = NewLogger(os.Stderr, &slog.LevelVar{})
	}
	if deps.InSession == nil {
		deps.InSession = inbox.InSession
	}
	if deps.Geteuid == nil {
		deps.Geteuid = os.Geteuid
	}
	if deps.ReadPeerCred == nil {
		deps.ReadPeerCred = ReadPeerCred
	}
	if deps.LocalProtocolVersion == 0 {
		deps.LocalProtocolVersion = localpb.ProtocolVersion
	}

	allowSameAccount := func(c Caller) bool {
		return c.UID == uint32(deps.Geteuid())
	}
	logReject := func(c Caller) {
		deps.Logger.Warn(EvConnectionRejected, "uid", c.UID, "pid", c.PID)
	}
	peerCreds := newPeerCredCreds(deps.ReadPeerCred, allowSameAccount, logReject)

	// The peer credentials are the server's credentials. A caller-supplied
	// credentials option would replace them because the last option wins, so it
	// is rejected rather than silently disabling the account check.
	if err := checkServerOptions(peerCreds, deps.ServerOptions); err != nil {
		return nil, err
	}

	opts := []grpc.ServerOption{
		grpc.Creds(peerCreds),
	}
	opts = append(opts, versionServerOptions()...)
	opts = append(opts, deps.ServerOptions...)

	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{
		deps:   deps,
		log:    deps.Logger,
		grpc:   grpc.NewServer(opts...),
		ctx:    ctx,
		cancel: cancel,
	}
	localpb.RegisterLocalServer(b.grpc, b)
	return b, nil
}

// checkServerOptions rejects Deps.ServerOptions that set transport credentials.
// New installs the process-credential handshake itself, and grpc.Creds applied
// later would replace it silently, letting any account connect to a user-level
// Broker.
func checkServerOptions(base credentials.TransportCredentials, opts []grpc.ServerOption) error {
	if len(opts) == 0 {
		return nil
	}
	if serverOptionsReplaceCreds(base, opts) {
		return fmt.Errorf("broker: Deps.ServerOptions must not set transport credentials; New installs the peer credentials")
	}
	return nil
}

// serverOptionsReplaceCreds applies base and then opts to a probe server and
// reports whether the resolved credentials are no longer base. grpc.ServerOption
// has no accessors, so the probe's resolved field is read through reflection; it
// is an interface field, and comparing the concrete pointer does not expose the
// unexported value. A nil or unrecognised field fails closed.
func serverOptionsReplaceCreds(base credentials.TransportCredentials, opts []grpc.ServerOption) bool {
	all := make([]grpc.ServerOption, 0, len(opts)+1)
	all = append(all, grpc.Creds(base))
	all = append(all, opts...)
	probe := grpc.NewServer(all...)
	defer probe.Stop()

	creds := reflect.ValueOf(probe).Elem().FieldByName("opts").FieldByName("creds")
	if !creds.IsValid() || creds.IsNil() {
		return true
	}
	// The peer credentials are a pointer. A different dynamic type means some
	// option replaced them; the same type means the same kind of value, which
	// for a pointer is compared by identity.
	elem := creds.Elem()
	if elem.Type() != reflect.TypeOf(base) {
		return true
	}
	return elem.Pointer() != reflect.ValueOf(base).Pointer()
}

// Serve serves the Local service on ln. It may be called once per listener;
// each call blocks until Stop.
func (b *Broker) Serve(ln net.Listener) error {
	return b.grpc.Serve(ln)
}

// Stop cancels the Broker's lifetime context and stops the gRPC server. It is
// safe to call more than once.
func (b *Broker) Stop() {
	b.cancel()
	b.grpc.Stop()
}

// Logger returns the Broker's structured logger.
func (b *Broker) Logger() *slog.Logger { return b.log }
