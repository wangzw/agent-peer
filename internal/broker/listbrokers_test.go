package broker

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/config"
	"github.com/wangzw/agent-peer/internal/pairing"
)

// listBrokersPeers returns the entries ListBrokers marks as peers, in order.
func listBrokersPeers(brokers []*localpb.BrokerInfo) []*localpb.BrokerInfo {
	out := make([]*localpb.BrokerInfo, 0, len(brokers))
	for _, info := range brokers {
		if info.GetIsPeer() {
			out = append(out, info)
		}
	}
	return out
}

// TestListBrokersReportsLocalOffline checks that ListBrokers always sets local
// and carries the current broker.offline value (REQ-broker-config-068).
func TestListBrokersReportsLocalOffline(t *testing.T) {
	tb := newTestBroker(t)

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	if resp.GetLocal() == nil {
		t.Fatal("ListBrokers leaves local unset")
	}
	if resp.GetLocal().GetOffline() {
		t.Fatal("ListBrokers reports offline while broker.offline is false")
	}

	tb.cfg.setValue("broker.offline", config.Value{Kind: config.KindBool, Bool: true})
	resp, err = tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers after going offline: %v", err)
	}
	if !resp.GetLocal().GetOffline() {
		t.Fatal("ListBrokers does not report offline while broker.offline is true")
	}
}

func TestListBrokersNeedsNoAgent(t *testing.T) {
	// REQ-broker-config-023: listing brokers does not depend on an agent
	// identity or on any configuration permission.
	p := &fakePairing{peers: []pairing.Peer{
		{Fingerprint: fp(2), Alias: "hostB", Role: pairing.RoleDialer},
	}}
	l := &fakePeerLinks{}
	tb := newTestBroker(t, withPeers(p, l))

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	if len(resp.GetBrokers()) != 1 {
		t.Fatalf("brokers = %v, want the one peer", resp.GetBrokers())
	}
	got := resp.GetBrokers()[0]
	if got.GetAlias() != "hostB" || !got.GetIsPeer() {
		t.Fatalf("broker = %v, want the peer hostB", got)
	}
	if got.GetRole() != localpb.PeerRole_PEER_ROLE_DIALER {
		t.Fatalf("role = %v, want DIALER", got.GetRole())
	}
}

func TestListBrokersDialerAndAcceptor(t *testing.T) {
	// REQ-broker-discovery-012, REQ-broker-discovery-014: peers carry their
	// addresses, their role and their peer flag.
	p := &fakePairing{peers: []pairing.Peer{
		{
			Fingerprint: fp(2),
			Alias:       "hostB",
			Role:        pairing.RoleDialer,
			Candidates: []pairing.Candidate{
				{Source: pairing.SourceAcceptor, Addr: "203.0.113.5:17443"},
			},
		},
		{Fingerprint: fp(3), Alias: "hostC", Role: pairing.RoleAcceptor},
	}}
	l := &fakePeerLinks{status: map[pairing.Fingerprint]PeerLinkStatus{
		fp(2): {Phase: LinkEstablished},
		fp(3): {Phase: LinkWaiting},
	}}
	tb := newTestBroker(t, withPeers(p, l))

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	brokers := resp.GetBrokers()
	if len(brokers) != 2 {
		t.Fatalf("brokers = %v, want hostB and hostC", brokers)
	}
	dialer, acceptor := brokers[0], brokers[1]
	if dialer.GetAlias() != "hostB" || acceptor.GetAlias() != "hostC" {
		t.Fatalf("order = %q, %q; want hostB, hostC", dialer.GetAlias(), acceptor.GetAlias())
	}
	if dialer.GetRole() != localpb.PeerRole_PEER_ROLE_DIALER || !dialer.GetIsPeer() {
		t.Fatalf("hostB role/is_peer = %v/%v, want DIALER/true", dialer.GetRole(), dialer.GetIsPeer())
	}
	if acceptor.GetRole() != localpb.PeerRole_PEER_ROLE_ACCEPTOR || !acceptor.GetIsPeer() {
		t.Fatalf("hostC role/is_peer = %v/%v, want ACCEPTOR/true", acceptor.GetRole(), acceptor.GetIsPeer())
	}
	if len(dialer.GetAdvertisedAddresses()) == 0 {
		t.Fatal("hostB has no advertised addresses, want the acceptor's")
	}
	if len(acceptor.GetAdvertisedAddresses()) != 0 {
		t.Fatalf("hostC addresses = %v, want none", acceptor.GetAdvertisedAddresses())
	}
	if dialer.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTED {
		t.Fatalf("hostB status = %v, want CONNECTED", dialer.GetStatus())
	}
	if acceptor.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_NOT_DIRECTLY_REACHABLE {
		t.Fatalf("hostC status = %v, want NOT_DIRECTLY_REACHABLE", acceptor.GetStatus())
	}
}

func TestListBrokersAfterUnpair(t *testing.T) {
	// REQ-broker-discovery-014: the list follows the current peer set; it is
	// not a cache of the peers present when the Broker started.
	p := &fakePairing{peers: []pairing.Peer{
		{Fingerprint: fp(2), Alias: "hostB", Role: pairing.RoleDialer},
		{Fingerprint: fp(3), Alias: "hostC", Role: pairing.RoleAcceptor},
	}}
	l := &fakePeerLinks{}
	tb := newTestBroker(t, withPeers(p, l))

	if err := p.Unpair(fp(2)); err != nil {
		t.Fatalf("Unpair: %v", err)
	}
	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	for _, info := range resp.GetBrokers() {
		if proto.Equal(&localpb.BrokerInfo{Fingerprint: info.GetFingerprint()},
			&localpb.BrokerInfo{Fingerprint: fingerprintBytes(2)}) {
			t.Fatalf("brokers = %v, still contains the unpaired hostB", resp.GetBrokers())
		}
	}
	if len(resp.GetBrokers()) != 1 || resp.GetBrokers()[0].GetAlias() != "hostC" {
		t.Fatalf("brokers = %v, want only hostC", resp.GetBrokers())
	}
}

func TestListBrokersMergesNonPeers(t *testing.T) {
	// REQ-broker-discovery-012: card holders that are not peers appear too,
	// forced to the non-peer shape, and a peer never appears twice.
	p := &fakePairing{peers: []pairing.Peer{
		{
			Fingerprint: fp(2),
			Alias:       "hostB",
			Role:        pairing.RoleDialer,
			Candidates: []pairing.Candidate{
				{Source: pairing.SourceAcceptor, Addr: "203.0.113.5:17443"},
			},
		},
		{Fingerprint: fp(3), Alias: "hostC", Role: pairing.RoleAcceptor},
	}}
	l := &fakePeerLinks{status: map[pairing.Fingerprint]PeerLinkStatus{
		fp(2): {Phase: LinkEstablished},
	}}
	catalog := &fakeCatalog{nonPeers: []*localpb.BrokerInfo{
		{
			Alias:       "hostD",
			Fingerprint: fingerprintBytes(4),
			IsPeer:      true,
			Role:        localpb.PeerRole_PEER_ROLE_DIALER,
			Status:      localpb.BrokerStatus_BROKER_STATUS_CONNECTED,
			LinkState:   localpb.LinkState_LINK_STATE_ESTABLISHED,
		},
		{
			// A stale duplicate of the peer hostB: the peer entry wins.
			Alias:       "hostB",
			Fingerprint: fingerprintBytes(2),
			IsPeer:      true,
			Role:        localpb.PeerRole_PEER_ROLE_DIALER,
			LinkState:   localpb.LinkState_LINK_STATE_HANDSHAKING,
		},
	}}
	tb := newTestBroker(t, withPeers(p, l), func(d *Deps) { d.Catalog = catalog })

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	brokers := resp.GetBrokers()
	if len(brokers) != 3 {
		t.Fatalf("brokers = %v, want hostB, hostC, hostD", brokers)
	}
	if brokers[0].GetAlias() != "hostB" || brokers[1].GetAlias() != "hostC" || brokers[2].GetAlias() != "hostD" {
		t.Fatalf("order = %q, %q, %q; want hostB, hostC, hostD",
			brokers[0].GetAlias(), brokers[1].GetAlias(), brokers[2].GetAlias())
	}
	var hostBCount int
	for _, info := range brokers {
		if proto.Equal(&localpb.BrokerInfo{Fingerprint: info.GetFingerprint()},
			&localpb.BrokerInfo{Fingerprint: fingerprintBytes(2)}) {
			hostBCount++
		}
	}
	if hostBCount != 1 {
		t.Fatalf("hostB appears %d times, want once", hostBCount)
	}
	if brokers[0].GetLinkState() != localpb.LinkState_LINK_STATE_ESTABLISHED {
		t.Fatalf("hostB link_state = %v, want the peer's ESTABLISHED", brokers[0].GetLinkState())
	}
	nonPeer := brokers[2]
	if nonPeer.GetIsPeer() {
		t.Fatalf("hostD is_peer = true, want false")
	}
	if nonPeer.GetRole() != localpb.PeerRole_PEER_ROLE_UNSPECIFIED {
		t.Fatalf("hostD role = %v, want UNSPECIFIED", nonPeer.GetRole())
	}
	if nonPeer.GetLinkState() != localpb.LinkState_LINK_STATE_UNSPECIFIED {
		t.Fatalf("hostD link_state = %v, want UNSPECIFIED", nonPeer.GetLinkState())
	}
}

func TestStatusPeersMatchListBrokers(t *testing.T) {
	// REQ-broker-config-010: Status reports the same peers as ListBrokers and
	// leaves the card holders that are not peers to ListBrokers. Status
	// forwards Deps.Peers verbatim, so this compares that exact value.
	p := &fakePairing{peers: []pairing.Peer{
		{
			Fingerprint: fp(2),
			Alias:       "hostB",
			Role:        pairing.RoleDialer,
			Candidates: []pairing.Candidate{
				{Source: pairing.SourceAcceptor, Addr: "203.0.113.5:17443"},
			},
		},
		{Fingerprint: fp(3), Alias: "hostC", Role: pairing.RoleAcceptor},
	}}
	l := &fakePeerLinks{status: map[pairing.Fingerprint]PeerLinkStatus{
		fp(2): {Phase: LinkEstablished},
		fp(3): {Phase: LinkWaiting},
	}}
	catalog := &fakeCatalog{
		cards: map[pairing.Fingerprint][]config.AdvertisedAddr{
			fp(3): {{Kind: config.AdvertisedKindDirect, Addr: "198.51.100.4:17443"}},
		},
		nonPeers: []*localpb.BrokerInfo{
			{Alias: "hostD", Fingerprint: fingerprintBytes(4)},
		},
	}
	tb := newTestBroker(t,
		withPeers(p, l),
		func(d *Deps) {
			d.Catalog = catalog
			// The production assembler wires the view to the live Deps; do
			// the same so Status and ListBrokers read one catalog.
			d.Peers = newPeerViewFunc(p, l, func() BrokerCatalog { return d.Catalog })
		},
	)

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	listPeers := listBrokersPeers(resp.GetBrokers())
	if len(resp.GetBrokers()) != 3 {
		t.Fatalf("brokers = %v, want the two peers and hostD", resp.GetBrokers())
	}

	statusPeers := tb.b.deps.Peers.Peers(context.Background())
	if len(statusPeers) != len(listPeers) {
		t.Fatalf("Status peers = %d, ListBrokers peers = %d", len(statusPeers), len(listPeers))
	}
	for i := range listPeers {
		if !proto.Equal(statusPeers[i], listPeers[i]) {
			t.Fatalf("peer %d: Status = %v, ListBrokers = %v", i, statusPeers[i], listPeers[i])
		}
	}

	// hostC has a directory card: its address comes from the card, and the
	// definition of CONNECTABLE only needs a known address, even though this
	// Broker never dials the acceptor.
	var hostC *localpb.BrokerInfo
	for _, info := range listPeers {
		if info.GetAlias() == "hostC" {
			hostC = info
		}
	}
	if hostC == nil {
		t.Fatalf("ListBrokers peers = %v, want hostC", listPeers)
	}
	wantAddrs := []*localpb.AdvertisedAddress{{Kind: "direct", Addr: "198.51.100.4:17443"}}
	if !proto.Equal(&localpb.BrokerInfo{AdvertisedAddresses: hostC.GetAdvertisedAddresses()},
		&localpb.BrokerInfo{AdvertisedAddresses: wantAddrs}) {
		t.Fatalf("hostC addresses = %v, want the card address %v", hostC.GetAdvertisedAddresses(), wantAddrs)
	}
	if hostC.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTABLE {
		t.Fatalf("hostC status = %v, want CONNECTABLE", hostC.GetStatus())
	}
	for _, info := range statusPeers {
		if proto.Equal(&localpb.BrokerInfo{Fingerprint: info.GetFingerprint()},
			&localpb.BrokerInfo{Fingerprint: fingerprintBytes(4)}) {
			t.Fatalf("Status peers = %v, contains the non-peer hostD", statusPeers)
		}
	}
}

func TestListBrokersWithoutPairing(t *testing.T) {
	// Without pairing there are no peers, but the card holders still list.
	catalog := &fakeCatalog{nonPeers: []*localpb.BrokerInfo{
		{
			Alias:       "hostD",
			Fingerprint: fingerprintBytes(4),
			IsPeer:      true,
			Role:        localpb.PeerRole_PEER_ROLE_DIALER,
		},
	}}
	tb := newTestBroker(t, func(d *Deps) {
		d.Pairing = nil
		d.PeerLinks = nil
		d.Peers = nil
		d.Catalog = catalog
	})

	resp, err := tb.client.ListBrokers(context.Background(), &localpb.ListBrokersRequest{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}
	if len(resp.GetBrokers()) != 1 {
		t.Fatalf("brokers = %v, want only hostD", resp.GetBrokers())
	}
	got := resp.GetBrokers()[0]
	if got.GetAlias() != "hostD" || got.GetIsPeer() {
		t.Fatalf("broker = %v, want the non-peer hostD", got)
	}
	if got.GetRole() != localpb.PeerRole_PEER_ROLE_UNSPECIFIED {
		t.Fatalf("role = %v, want UNSPECIFIED", got.GetRole())
	}
}
