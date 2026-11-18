package broker

import (
	"context"
	"fmt"
	"net"
	"os/user"
	"strconv"
	"sync/atomic"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Caller is the account and process on the other end of a local connection.
type Caller struct {
	UID      uint32
	GID      uint32
	PID      int32
	Username string
}

// ReadPeerCred reads the process credentials of the peer of conn. It is called
// once per connection during the gRPC handshake.
func ReadPeerCred(conn net.Conn) (Caller, error) {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return Caller{}, fmt.Errorf("peercred: connection is %T, not *net.UnixConn", conn)
	}
	return readPeerCred(u)
}

// usernameFor returns the account name for uid, or the decimal uid when the
// account cannot be looked up.
func usernameFor(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil && u.Username != "" {
		return u.Username
	}
	return id
}

// callerAuthInfo is the AuthInfo of one local connection.
type callerAuthInfo struct {
	credentials.CommonAuthInfo
	caller Caller
	// rejected is set by Hello after a version mismatch; checked by the
	// version interceptors.
	rejected atomic.Bool
	// rejectedClientVersion is the client version of the rejected Hello; valid
	// when rejected is set.
	rejectedClientVersion atomic.Uint32
	// rejectedBrokerVersion is this Broker's version at that Hello; valid when
	// rejected is set.
	rejectedBrokerVersion atomic.Uint32
}

// AuthType implements credentials.AuthInfo.
func (a *callerAuthInfo) AuthType() string { return "peercred" }

// authInfoFrom returns the caller's per-connection AuthInfo. It is the hook the
// version-compat unit uses to set the rejected flag.
func authInfoFrom(ctx context.Context) (*callerAuthInfo, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil, false
	}
	info, ok := p.AuthInfo.(*callerAuthInfo)
	return info, ok
}

// CallerFrom returns the caller identified on the connection that carries ctx.
func CallerFrom(ctx context.Context) (Caller, bool) {
	info, ok := authInfoFrom(ctx)
	if !ok {
		return Caller{}, false
	}
	return info.caller, true
}

// peerCredCreds identifies the caller of every local connection.
type peerCredCreds struct {
	read     func(net.Conn) (Caller, error)
	allow    func(Caller) bool
	onReject func(Caller)
}

func newPeerCredCreds(read func(net.Conn) (Caller, error), allow func(Caller) bool, onReject func(Caller)) credentials.TransportCredentials {
	return &peerCredCreds{read: read, allow: allow, onReject: onReject}
}

// ClientHandshake implements credentials.TransportCredentials.
func (c *peerCredCreds) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, fmt.Errorf("peercred: client side not supported")
}

// ServerHandshake implements credentials.TransportCredentials.
func (c *peerCredCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	caller, err := c.read(conn)
	if err != nil {
		return nil, nil, err
	}
	if c.allow != nil && !c.allow(caller) {
		if c.onReject != nil {
			c.onReject(caller)
		}
		return nil, nil, fmt.Errorf("account %d is not served by this Broker", caller.UID)
	}
	info := &callerAuthInfo{
		CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.NoSecurity},
		caller:         caller,
	}
	return conn, info, nil
}

// Info implements credentials.TransportCredentials.
func (c *peerCredCreds) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "peercred"}
}

// Clone implements credentials.TransportCredentials.
func (c *peerCredCreds) Clone() credentials.TransportCredentials {
	clone := *c
	return &clone
}

// OverrideServerName implements credentials.TransportCredentials.
func (c *peerCredCreds) OverrideServerName(string) error { return nil }
