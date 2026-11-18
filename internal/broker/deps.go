package broker

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"google.golang.org/grpc"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// ErrInvalidTopicName is returned by TopicTable.Join and Leave for a name that
// breaks the topic name rules; the broker maps it to invalid_argument.
var ErrInvalidTopicName = errors.New("broker: invalid topic name")

// ErrUnknownAgent is returned by TopicTable.Join for an agent that AgentChanged
// never introduced or that LeaveAll removed; the broker maps it to internal.
var ErrUnknownAgent = errors.New("broker: agent not introduced to the topic table")

// Member is one agent's current roster entry in the topic table.
type Member struct {
	Name     string
	Ref      string
	Status   string
	PrevName string
}

// VisibleAgent is one agent visible to a local agent: the local agent itself
// for a local entry, or a peer member for a remote entry.
type VisibleAgent struct {
	Local  AgentID  // non-empty for a local agent
	Peer   [32]byte // fingerprint of the peer Broker for a remote agent
	Member Member   // the agent's current roster entry (local and remote)
	Shared []string // shared topics, sorted
}

// TopicTable is the broker's view of the topic registry. The topic package is
// not imported here; its assembler supplies an adapter once it lands.
type TopicTable interface {
	Join(agent AgentID, name string) error
	Leave(agent AgentID, name string) error
	LeaveAll(agent AgentID)
	TopicsOf(agent AgentID) []string
	AgentChanged(agent AgentID, name, prevName, ref, status string)
	Visible(local AgentID, other VisibleAgent) (shared []string, ok bool)
	VisibleAgents(local AgentID) []VisibleAgent // excludes local itself
	RemoteMembers(peer [32]byte) []Member
}

// PublicTopic is one public topic declaration.
type PublicTopic struct {
	Name        string
	Description string
}

// PublicTopicDirectory is the broker's view of the directory's public topic
// declarations. DeclarePublicTopic returns an *Error or a plain error; the
// broker passes it to the client unchanged except for plain-error mapping.
type PublicTopicDirectory interface {
	DeclarePublicTopic(name, description string) error
	RetirePublicTopic(name string)
	PublicTopics() []PublicTopic
}

// PeerView is the broker's view of paired Brokers: their effective local
// aliases and their information for Status.
type PeerView interface {
	EffectiveAlias(fp [32]byte) (alias string, ok bool)
	Peers(ctx context.Context) []*localpb.BrokerInfo
}

// LinkRunner is the lifecycle of link that Run drives (startup steps 6 and 9,
// shutdown).
type LinkRunner interface {
	Listen(address string) error
	Start()
	Close()
}

// Component is a part of the Broker started after New and before the link
// starts listening. Start registers RPC handlers, event subscriptions and link
// frame handling; Stop releases them.
type Component interface {
	Name() string
	Start(ctx context.Context, b *Broker) error
	Stop()
}

// PostReadyComponent is a Component that runs additional work after the local
// socket is ready and the link has started.
type PostReadyComponent interface {
	Component
	AfterReady(ctx context.Context) error
}

// Deps is everything New needs from the process entry point and the assemblers.
type Deps struct {
	Config               ConfigSource                   // nil in Run: PackageConfig()
	Sessions             SessionRouter                  // nil: UserRouter()
	Topics               TopicTable                     // required
	Directory            PublicTopicDirectory           // nil: built-in "not public"
	Peers                PeerView                       // nil: no peers
	Clock                Clock                          // nil: SystemClock()
	Logger               *slog.Logger                   // nil: NewLogger(os.Stderr, info)
	InSession            func() bool                    // nil: inbox.InSession
	Geteuid              func() int                     // nil: os.Geteuid
	ReadPeerCred         func(net.Conn) (Caller, error) // nil: ReadPeerCred
	ServerOptions        []grpc.ServerOption            // appended to the gRPC server options (e.g. the access interceptor)
	LocalProtocolVersion uint32                         // 0: localpb.ProtocolVersion; Run copies RunOptions.LocalProtocolVersion here

	Pairing   PairingOps    // nil: peer management RPCs return internal; ListBrokers lists only the catalog's non-peers
	PeerLinks PeerLinks     // nil: peer management RPCs return internal; ListBrokers lists only the catalog's non-peers
	Catalog   BrokerCatalog // nil: no directory
	Warner    ConnectWarner // nil: no warnings

	Delivery *delivery // nil: Send returns internal
}
