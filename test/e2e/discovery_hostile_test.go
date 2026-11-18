//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/directory/directorypb"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/internal/pairing"
	"github.com/wangzw/agent-peer/test/e2e/fakepeer/fakerec"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// hostileRecordTTL is comfortably inside the directory's seven-day lifetime,
// so a record is rejected only for the reason a case tests.
const hostileRecordTTL = 168 * time.Hour

// hostilePairKey creates a one-time pairing key through an operator session.
func hostilePairKey(t *testing.T, d *discNode) string {
	t.Helper()
	op := discOperator(t, d)
	defer op.End(t)
	r := discCallOK(t, op, "create_pairing_key", nil, 3*time.Second)
	key, err := harness.Decode[mcpserver.CreatePairingKeyOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding create_pairing_key on %s: %v", d.name, err)
	}
	return key.Key
}

// hostileBuild builds a directory record with the fields a case does not set
// filled in.
func hostileBuild(t *testing.T, spec fakerec.Spec) *directorypb.Record {
	t.Helper()
	if spec.Version == 0 {
		spec.Version = 1
	}
	if spec.ExpiresIn == 0 {
		spec.ExpiresIn = hostileRecordTTL
	}
	record, err := fakerec.Build(spec, time.Now())
	if err != nil {
		t.Fatalf("building a fake record: %v", err)
	}
	return record
}

// hostileCard builds a card record.
func hostileCard(t *testing.T, seed, alias, address, tamper string) *directorypb.Record {
	t.Helper()
	return hostileBuild(t, fakerec.Spec{
		Seed:      seed,
		Kind:      "card",
		Alias:     alias,
		Addresses: []fakerec.Addr{{Kind: "direct", Addr: address}},
		Tamper:    tamper,
	})
}

// hostileHexFingerprint renders a publisher fingerprint as lowercase hex.
func hostileHexFingerprint(fp pairing.Fingerprint) string {
	return hex.EncodeToString(fp[:])
}

// hostileNonPeers returns the entries that are not paired peers.
func hostileNonPeers(brokers []*localpb.BrokerInfo) []*localpb.BrokerInfo {
	out := make([]*localpb.BrokerInfo, 0, len(brokers))
	for _, broker := range brokers {
		if !broker.GetIsPeer() {
			out = append(out, broker)
		}
	}
	return out
}

// hostileAbsent observes for the whole window that d lists none of the
// fingerprints.
func hostileAbsent(t *testing.T, d *discNode, window time.Duration, what string, fingerprints ...string) {
	t.Helper()
	harness.Consistently(t, window, what, func() (bool, string) {
		brokers, state := discList(t, d)
		if state != "" {
			return false, state
		}
		for _, broker := range brokers {
			got := hex.EncodeToString(broker.GetFingerprint())
			for _, want := range fingerprints {
				if got == want {
					return false, "fingerprint " + got + " is listed"
				}
			}
		}
		return true, ""
	})
}

// TestDirectoryRejectsTamperedCards checks that a tampered card and an
// impersonated card are dropped while a valid card from the same unpaired
// (but paired) source propagates: the signature covers the payload and the
// publisher must match the presented public key.
func TestDirectoryRejectsTamperedCards(t *testing.T) {
	t.Parallel()

	b := discNewNode(t, "hostile-b", "", false)
	c := discNewNode(t, "hostile-c", "", false)
	discPair(t, b, c)

	f := startFakePeer(t)
	f.Pair(t, b.addr, hostilePairKey(t, b))
	f.WaitLinked(t, 3*time.Second)

	const (
		goodSeed         = "tamper-good"
		tamperedSeed     = "tamper-addresses"
		impersonatedSeed = "tamper-publisher"
	)
	good := hostileCard(t, goodSeed, "card-good", "198.51.100.10:17443", "")
	tampered := hostileCard(t, tamperedSeed, "card-tampered", "198.51.100.11:17443", "addresses")
	impersonated := hostileCard(t, impersonatedSeed, "card-impersonated", "198.51.100.12:17443", "publisher")
	f.Records(t, good, tampered, impersonated)

	fpGood := hostileHexFingerprint(fakerec.PublisherOf(goodSeed))
	fpTampered := hostileHexFingerprint(fakerec.PublisherOf(tamperedSeed))
	fpImpersonated := hostileHexFingerprint(fakerec.PublisherOf(impersonatedSeed))
	claimed := pairing.Fingerprint(sha256.Sum256([]byte("other:" + impersonatedSeed)))
	fpClaimed := hostileHexFingerprint(claimed)

	for _, node := range []*discNode{b, c} {
		discWaitBroker(t, node, fpGood, discBound(1), func(broker *localpb.BrokerInfo) string {
			if broker.GetAlias() != "card-good" {
				return fmt.Sprintf("alias is %q, want card-good", broker.GetAlias())
			}
			if broker.GetIsPeer() {
				return "the control card is listed as a peer"
			}
			return ""
		})
	}

	for _, node := range []*discNode{b, c} {
		hostileAbsent(t, node, discBound(2), node.name+" rejects the tampered and impersonated cards", fpTampered, fpImpersonated, fpClaimed)
	}
}

// TestDirectoryRejectsUnpairedSource checks that records pushed over a stream
// that never completed pairing are dropped, while the same records are
// accepted once the source is paired.
func TestDirectoryRejectsUnpairedSource(t *testing.T) {
	t.Parallel()

	b := discNewNode(t, "unpaired-b", "", false)
	f := startFakePeer(t)

	const seed = "unpaired-card"
	card := hostileCard(t, seed, "unpaired-card", "198.51.100.20:17443", "")
	f.Records(t, card)

	reason := f.DialUnpaired(t, b.addr)
	if reason == "" {
		t.Fatal("the unpaired stream closed without a reason")
	}

	fp := hostileHexFingerprint(fakerec.PublisherOf(seed))
	hostileAbsent(t, b, discBound(1), "B rejects records from an unpaired source", fp)

	f.Pair(t, b.addr, hostilePairKey(t, b))
	f.WaitLinked(t, 3*time.Second)
	f.Records(t, card)

	discWaitBroker(t, b, fp, discBound(1), func(broker *localpb.BrokerInfo) string {
		if broker.GetAlias() != "unpaired-card" {
			return fmt.Sprintf("alias is %q, want unpaired-card", broker.GetAlias())
		}
		return ""
	})
}

// TestDirectoryPerLinkLimit checks that one link cannot fill the directory:
// a link stops at directory.max_records_per_link while a second link still
// adds its record under the total limit.
func TestDirectoryPerLinkLimit(t *testing.T) {
	t.Parallel()

	b := discNewNode(t, "limit-b", "directory:\n  max_records: 10\n  max_records_per_link: 4", false)

	first := startFakePeer(t)
	first.Pair(t, b.addr, hostilePairKey(t, b))
	first.WaitLinked(t, 3*time.Second)
	second := startFakePeer(t)
	second.Pair(t, b.addr, hostilePairKey(t, b))
	second.WaitLinked(t, 3*time.Second)

	records := make([]*directorypb.Record, 0, 10)
	for i := 0; i < 10; i++ {
		seed := fmt.Sprintf("limit-%d", i)
		records = append(records, hostileCard(t, seed, seed, fmt.Sprintf("198.51.100.%d:17443", 30+i), ""))
	}
	first.Records(t, records...)

	harness.Eventually(t, discBound(1), "B holds exactly four records from the first link", func() (bool, string) {
		brokers, state := discList(t, b)
		if state != "" {
			return false, state
		}
		if got := len(hostileNonPeers(brokers)); got != 4 {
			return false, fmt.Sprintf("%d non-peer records", got)
		}
		return true, ""
	})
	harness.Consistently(t, discBound(1), "B keeps the first link at four records", func() (bool, string) {
		brokers, state := discList(t, b)
		if state != "" {
			return false, state
		}
		if got := len(hostileNonPeers(brokers)); got != 4 {
			return false, fmt.Sprintf("%d non-peer records", got)
		}
		return true, ""
	})

	const extraSeed = "limit-extra"
	second.Records(t, hostileCard(t, extraSeed, extraSeed, "198.51.100.99:17443", ""))
	fpExtra := hostileHexFingerprint(fakerec.PublisherOf(extraSeed))
	discWaitBroker(t, b, fpExtra, discBound(1), nil)
	brokers, state := discList(t, b)
	if state != "" {
		t.Fatalf("B's ListBrokers failed: %s", state)
	}
	if got := len(hostileNonPeers(brokers)); got != 5 {
		t.Fatalf("B lists %d non-peer records after the second link, want 5", got)
	}
}

// TestDirectoryUnknownAddressKind checks that a card carrying an address kind
// this version does not know is accepted, shown without that entry and still
// propagated.
func TestDirectoryUnknownAddressKind(t *testing.T) {
	t.Parallel()

	b := discNewNode(t, "kind-b", "", false)
	c := discNewNode(t, "kind-c", "", false)
	discPair(t, b, c)

	f := startFakePeer(t)
	f.Pair(t, b.addr, hostilePairKey(t, b))
	f.WaitLinked(t, 3*time.Second)

	const seed = "unknown-kind"
	record := hostileBuild(t, fakerec.Spec{
		Seed:  seed,
		Kind:  "card",
		Alias: "card-kind",
		Addresses: []fakerec.Addr{
			{Kind: "relay", Addr: "relay.example:1"},
			{Kind: "direct", Addr: "198.51.100.4:17443"},
		},
	})
	f.Records(t, record)

	fp := hostileHexFingerprint(fakerec.PublisherOf(seed))
	for _, node := range []*discNode{b, c} {
		discWaitBroker(t, node, fp, discBound(1), func(broker *localpb.BrokerInfo) string {
			addrs := broker.GetAdvertisedAddresses()
			if len(addrs) != 1 || addrs[0].GetKind() != "direct" || addrs[0].GetAddr() != "198.51.100.4:17443" {
				return fmt.Sprintf("addresses are %v, want the direct entry only", addrs)
			}
			if broker.GetStatus() != localpb.BrokerStatus_BROKER_STATUS_CONNECTABLE {
				return fmt.Sprintf("status is %v, want CONNECTABLE", broker.GetStatus())
			}
			return ""
		})
	}
}
