package topic

import (
	"log/slog"
	"sync"

	"github.com/wangzw/peers/internal/link"
	"google.golang.org/protobuf/proto"
)

type memberKey struct{ Name, Ref string }

type localAgent struct {
	info   Member
	topics map[string]struct{}
}
type localTable struct {
	agents map[AgentID]*localAgent
	topics map[string]map[AgentID]struct{} // only non-empty topics
}

// newLocalTable returns an empty table with both maps allocated.
func newLocalTable() *localTable {
	return &localTable{
		agents: map[AgentID]*localAgent{},
		topics: map[string]map[AgentID]struct{}{},
	}
}

type peerState struct {
	salt    []byte                         // private copy
	digest  map[string][32]byte            // local non-empty topic -> digest under salt
	name    map[[32]byte]string            // reverse of digest
	remote  map[[32]byte]struct{}          // peer's latest TopicDigests
	shared  map[[32]byte]struct{}          // current intersection
	rosters map[[32]byte]map[string]Member // peer's entries per digest, keyed by Ref
}

// newPeerState copies salt and fills digest and name for every local topic;
// remote, shared and rosters start empty (allocated).
func newPeerState(salt []byte, localTopics []string) *peerState {
	ps := &peerState{
		salt:    append([]byte(nil), salt...),
		digest:  make(map[string][32]byte, len(localTopics)),
		name:    make(map[[32]byte]string, len(localTopics)),
		remote:  map[[32]byte]struct{}{},
		shared:  map[[32]byte]struct{}{},
		rosters: map[[32]byte]map[string]Member{},
	}
	for _, n := range localTopics {
		d := digest(ps.salt, n)
		ps.digest[n] = d
		ps.name[d] = n
	}
	return ps
}

// Registry is the topic table. It holds the local agents and their topics and,
// for every established link, that peer's salt, digest sets and roster. One
// mutex guards both tables; queries take the read side and return copies.
type Registry struct {
	mu    sync.RWMutex
	t     Transport
	log   *slog.Logger
	local *localTable
	peers map[link.Fingerprint]*peerState
}

// sendLocked encodes m and calls r.t.Send; on error it logs topic.send_failed
// at debug level (fields peer, kind, error) and returns. Callers hold r.mu.Lock.
func (r *Registry) sendLocked(peer link.Fingerprint, kind uint32, m proto.Message) {
	f, err := encodeFrame(kind, m)
	if err != nil {
		r.log.Debug("topic.send_failed", "peer", peer.String(), "kind", kind, "error", err)
		return
	}
	if err := r.t.Send(peer, f); err != nil {
		r.log.Debug("topic.send_failed", "peer", peer.String(), "kind", kind, "error", err)
	}
}
