package broker

import (
	"bytes"
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// peerInfos lists this Broker's peers as local API entries. ListBrokers and
// the peer view behind Status read the same pairing and link surfaces through
// peerView, so the two lists cannot drift apart.
func (b *Broker) peerInfos() []*localpb.BrokerInfo {
	return peerView{
		p:       b.deps.Pairing,
		l:       b.deps.PeerLinks,
		catalog: func() BrokerCatalog { return b.deps.Catalog },
	}.infos()
}

// ListBrokers returns every Broker this Broker knows: its peers and the
// non-peers the directory holds a card for. It needs no agent identity and no
// permission check; permission checks for the other calls are gRPC
// interceptors.
func (b *Broker) ListBrokers(_ context.Context, _ *localpb.ListBrokersRequest) (*localpb.ListBrokersResponse, error) {
	out := b.peerInfos()
	if b.deps.Catalog != nil {
		for _, info := range b.deps.Catalog.NonPeers() {
			if info == nil || b.knownPeer(info.GetFingerprint(), out) {
				continue
			}
			// Copy before forcing the peer-only fields so the catalog keeps
			// the entry it handed out.
			entry := proto.Clone(info).(*localpb.BrokerInfo)
			entry.IsPeer = false
			entry.Role = localpb.PeerRole_PEER_ROLE_UNSPECIFIED
			entry.LinkState = localpb.LinkState_LINK_STATE_UNSPECIFIED
			out = append(out, entry)
		}
	}
	sortBrokerInfos(out)
	return &localpb.ListBrokersResponse{
		Brokers: out,
		Local: &localpb.LocalBrokerState{
			Degraded: b.degradedState(),
			Offline:  b.offline(),
		},
	}, nil
}

// knownPeer reports whether the fingerprint already has an entry, or belongs
// to a peer that appeared after the peer list was built.
func (b *Broker) knownPeer(fingerprint []byte, infos []*localpb.BrokerInfo) bool {
	for _, info := range infos {
		if bytes.Equal(info.GetFingerprint(), fingerprint) {
			return true
		}
	}
	if b.deps.Pairing == nil || len(fingerprint) != len(pairing.Fingerprint{}) {
		return false
	}
	var fp pairing.Fingerprint
	copy(fp[:], fingerprint)
	return b.deps.Pairing.IsPeer(fp)
}
