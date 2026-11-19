package broker

import (
	"context"
	"sort"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// Status returns the local part of the process status: every local agent with
// its membership, plus the peers reported by Deps.Peers. It deliberately does
// not reread session records. A caller without read permission on the
// configuration file gets only the agents it owns; the peers and their
// connection states stay visible. Status needs no agent identity.
func (b *Broker) Status(ctx context.Context, req *localpb.StatusRequest) (*localpb.StatusResponse, error) {
	b.mu.Lock()
	agents := make([]*localpb.AgentStatusEntry, 0, len(b.agents))
	for _, a := range b.agents {
		topics := append([]string(nil), b.deps.Topics.TopicsOf(a.id)...)
		sort.Strings(topics)
		agents = append(agents, &localpb.AgentStatusEntry{
			AgentId:      string(a.id),
			Account:      a.caller.Username,
			Address:      b.addressOf(a),
			Topics:       topics,
			Status:       statusToProto(a.status),
			PreviousName: a.prevName,
		})
	}
	b.mu.Unlock()

	sort.Slice(agents, func(i, j int) bool { return agents[i].Address < agents[j].Address })

	var peers []*localpb.BrokerInfo
	if b.deps.Peers != nil {
		peers = b.deps.Peers.Peers(ctx)
	}

	if !readAccessGranted(ctx) {
		caller, ok := CallerFrom(ctx)
		kept := agents[:0]
		if ok {
			for _, entry := range agents {
				snapshot, found := b.lookupAgent(AgentID(entry.AgentId))
				if found && snapshot.Caller.UID == caller.UID {
					kept = append(kept, entry)
				}
			}
		}
		agents = kept
	}

	// The available version comes from the periodic check. It is empty for a
	// non-release build, which never checks.
	current := b.deps.Version
	available, _ := b.availableUpdate()
	if !isReleaseVersion(current) {
		available = ""
	}
	return &localpb.StatusResponse{
		Agents:           agents,
		Peers:            peers,
		AvailableVersion: available,
		CurrentVersion:   current,
	}, nil
}
