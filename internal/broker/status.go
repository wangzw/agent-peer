package broker

import (
	"context"
	"sort"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// Status returns the local part of the process status: every local agent with
// its membership, plus the peers reported by Deps.Peers. It deliberately does
// not reread session records; read-permission filtering is added by the
// configuration unit in this handler, not here. Status needs no agent identity.
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
	return &localpb.StatusResponse{Agents: agents, Peers: peers}, nil
}
