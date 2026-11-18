//go:build e2e

package e2e

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/test/e2e/harness"
)

const (
	// dxReconcileInterval is the directory reconciliation interval every node
	// in these tests runs with; one propagation hop costs at most one interval.
	dxReconcileInterval = time.Second

	// dxPropagationBound bounds a card crossing two hops: twice the
	// reconciliation interval plus the project-wide three seconds.
	dxPropagationBound = 2*dxReconcileInterval + 3*time.Second

	// dxExpiryBound bounds one Broker noticing an expired record: one
	// reconciliation interval plus three seconds.
	dxExpiryBound = dxReconcileInterval + 3*time.Second

	// dxQuietWindow is the observation window of a negative check. One
	// reconciliation interval has already passed and nothing else changes, so
	// the entry must stay absent for the whole window.
	dxQuietWindow = dxReconcileInterval + 3*time.Second

	// dxPublicTopic is the topic the expiry case declares publicly.
	dxPublicTopic = "infra-help"

	// dxClockEnv names the wall-clock offset file the agentpeer_testenv hook
	// reads.
	dxClockEnv = "AGENT_PEER_TEST_CLOCK_FILE"
)

// TestCardExpiresAfterSevenDays covers REQ-broker-discovery-007: a card issued
// by a public Broker stays visible for six days and disappears after seven.
// A is public and paired with B; C is paired with B and receives A's card
// through B. A stops serving before the clock moves, so only the expiry can
// remove the card.
func TestCardExpiresAfterSevenDays(t *testing.T) {
	aHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "expiry-a"})
	bHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "expiry-b"})
	cHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "expiry-c"})

	listenA := aHost.FreeAddr(t)
	listenB := bHost.FreeAddr(t)
	listenC := cHost.FreeAddr(t)

	clockB := dxClockFile(t)
	clockC := dxClockFile(t)

	dxStartBroker(t, aHost, dxConfig(listenA, true), nil)
	dxStartBroker(t, bHost, dxConfig(listenB, false),
		map[string]string{dxClockEnv: clockB})
	dxStartBroker(t, cHost, dxConfig(listenC, false),
		map[string]string{dxClockEnv: clockC})

	opA := adOperator(t, aHost)
	defer opA.End(t)
	opB := adOperator(t, bHost)
	defer opB.End(t)
	opC := adOperator(t, cHost)
	defer opC.End(t)

	fpA := dxPair(t, opA, opB, listenA)
	dxPair(t, opB, opC, listenB)
	dxWaitCard(t, cHost, fpA, []string{listenA}, dxPropagationBound)

	// A must not come back on its own, so end the session that could restart
	// it before stopping the Broker.
	opA.End(t)
	if code := aHost.StopBroker(t, syscall.SIGTERM); code != 0 {
		t.Fatalf("A's Broker exited with %d on SIGTERM, want 0", code)
	}

	// Six days after issuance the card is still live for every reader.
	dxSetClock(t, clockB, 144*time.Hour)
	dxSetClock(t, clockC, 144*time.Hour)
	harness.Consistently(t, dxQuietWindow, "C still lists A six days after issuance",
		func() (bool, string) {
			resp := dxListBrokers(t, cHost)
			if _, ok := dxFindBroker(resp, fpA); !ok {
				return false, "A is no longer listed"
			}
			return true, ""
		})

	// Eight days after issuance the record has expired. Nothing is left to
	// renew it because A is stopped, so every reader drops it.
	dxSetClock(t, clockB, 192*time.Hour)
	dxSetClock(t, clockC, 192*time.Hour)
	harness.Eventually(t, dxExpiryBound, "C drops A eight days after issuance",
		func() (bool, string) {
			resp := dxListBrokers(t, cHost)
			if _, ok := dxFindBroker(resp, fpA); ok {
				return false, "A is still listed"
			}
			return true, ""
		})
}

// TestCardRenewedDaily covers REQ-broker-discovery-008: while a public Broker
// runs, its card is renewed every day and never expires. A, B and C advance
// together, because a record issued on a clock far ahead of B's is rejected as
// too far in the future.
func TestCardRenewedDaily(t *testing.T) {
	aHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "renew-a"})
	bHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "renew-b"})
	cHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "renew-c"})

	listenA := aHost.FreeAddr(t)
	listenB := bHost.FreeAddr(t)
	listenC := cHost.FreeAddr(t)

	clock := dxClockFile(t)
	clockEnv := map[string]string{dxClockEnv: clock}

	dxStartBroker(t, aHost, dxConfig(listenA, true), clockEnv)
	dxStartBroker(t, bHost, dxConfig(listenB, false), clockEnv)
	dxStartBroker(t, cHost, dxConfig(listenC, false), clockEnv)

	opA := adOperator(t, aHost)
	defer opA.End(t)
	opB := adOperator(t, bHost)
	defer opB.End(t)
	opC := adOperator(t, cHost)
	defer opC.End(t)

	fpA := dxPair(t, opA, opB, listenA)
	dxPair(t, opB, opC, listenB)
	dxWaitCard(t, cHost, fpA, []string{listenA}, dxPropagationBound)

	// Eleven steps of 23 hours reach 253 hours. A renewal is due after 24
	// hours, so the card expires at step eight (184h) unless it is renewed.
	//
	// The check after each step spans the propagation window instead of
	// returning on the first look: a maintenance pass has to run at the new
	// offset for a due renewal to be issued, and only a window of more than
	// one reconciliation interval guarantees that. A card that is never
	// renewed vanishes at step eight even though it was seen after step seven.
	for step := 1; step <= 11; step++ {
		offset := time.Duration(step) * 23 * time.Hour
		dxSetClock(t, clock, offset)
		harness.Consistently(t, dxPropagationBound,
			fmt.Sprintf("C lists A after step %d (%s)", step, offset),
			func() (bool, string) {
				resp := dxListBrokers(t, cHost)
				broker, ok := dxFindBroker(resp, fpA)
				if !ok {
					return false, "the card is not listed"
				}
				if got := adPeerAddresses(broker); !adSameStrings(got, []string{listenA}) {
					return false, fmt.Sprintf("addresses are %v", got)
				}
				return true, ""
			})
	}
}

// TestPublicTopicExpiresWithDeclarer covers REQ-topics-012: a public topic
// declaration lives as long as its declarer's card. A declares the topic while
// public and is killed, so no session-end withdrawal removes the declaration
// first; B keeps it for six days and drops it once its clock passes the
// declaration's expiry.
func TestPublicTopicExpiresWithDeclarer(t *testing.T) {
	aHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "topic-expiry-a"})
	bHost := harness.NewLocalHost(t, harness.LocalOptions{Name: "topic-expiry-b"})

	listenA := aHost.FreeAddr(t)
	listenB := bHost.FreeAddr(t)
	clockB := dxClockFile(t)

	dxStartBroker(t, aHost, dxConfig(listenA, true), nil)
	dxStartBroker(t, bHost, dxConfig(listenB, false),
		map[string]string{dxClockEnv: clockB})

	opA := adOperator(t, aHost)
	defer opA.End(t)
	opB := adOperator(t, bHost)
	defer opB.End(t)

	dxPair(t, opA, opB, listenA)

	// list_topics requires a registered agent, so B reads it through a real
	// session rather than the management-only operator.
	b1 := bHost.StartSession(t, harness.SessionSpec{Name: "b1", StartMCP: true})
	defer b1.End(t)
	a1 := aHost.StartSession(t, harness.SessionSpec{Name: "a1", StartMCP: true})
	defer a1.End(t)
	harness.Eventually(t, 3*time.Second, "a1 declares "+dxPublicTopic+" publicly",
		func() (bool, string) {
			r := a1.Call(t, "join_topic", map[string]any{
				"name":        dxPublicTopic,
				"public":      true,
				"description": "ask infra",
			})
			if r.IsError {
				return false, r.Text
			}
			return true, ""
		})

	harness.Eventually(t, dxExpiryBound, "B lists "+dxPublicTopic,
		func() (bool, string) {
			out, state := dxListTopics(t, b1)
			if state != "" {
				return false, state
			}
			if !dxHasPublicTopic(out, dxPublicTopic) {
				return false, fmt.Sprintf("public topics are %v", out.Public)
			}
			return true, ""
		})

	// Kill A without ending a1: ending the session would withdraw the last
	// public member and the declaration would be gone before the clock moves.
	aHost.StopBroker(t, syscall.SIGKILL)

	// Six days after issuance the declaration is still within its seven-day
	// life and must stay visible.
	dxSetClock(t, clockB, 144*time.Hour)
	harness.Consistently(t, dxQuietWindow, "B still lists "+dxPublicTopic+" after 144h",
		func() (bool, string) {
			out, state := dxListTopics(t, b1)
			if state != "" {
				return false, state
			}
			if !dxHasPublicTopic(out, dxPublicTopic) {
				return false, fmt.Sprintf("public topics are %v", out.Public)
			}
			return true, ""
		})

	// Eight days after issuance the declaration has expired. A is dead, so
	// only the expiry can remove it.
	dxSetClock(t, clockB, 192*time.Hour)
	harness.Eventually(t, dxExpiryBound, "B drops "+dxPublicTopic+" with its declarer's card",
		func() (bool, string) {
			out, state := dxListTopics(t, b1)
			if state != "" {
				return false, state
			}
			if dxHasPublicTopic(out, dxPublicTopic) {
				return false, dxPublicTopic + " is still listed"
			}
			return true, ""
		})

	// The listing hides an expired record even before a maintenance pass runs.
	// The stored record itself must be purged too, so a later restart cannot
	// bring it back.
	harness.Eventually(t, dxExpiryBound, "B's directory file drops "+dxPublicTopic,
		func() (bool, string) {
			for key, body := range dxDirectoryRecords(t, bHost) {
				if body.GetTopicAnnouncement().GetName() == dxPublicTopic {
					return false, "the declaration is still stored for " + key.Publisher
				}
			}
			return true, ""
		})
}

// dxClockFile creates a clock offset file that starts at zero and returns its
// path. The Broker holding it must run a build with the agentpeer_testenv tag.
func dxClockFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clock")
	dxSetClock(t, path, 0)
	return path
}

// dxSetClock replaces the offset file atomically: the hook rereads the file
// when its modification time or size changes and never sees a partial value.
func dxSetClock(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(offset.String()), 0o644); err != nil {
		t.Fatalf("writing the clock offset: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("installing the clock offset: %v", err)
	}
}

// dxConfig builds one node's configuration: the shortened timers, the one
// second reconciliation interval, and one loopback address used both to listen
// and to advertise.
func dxConfig(listen string, public bool) string {
	parts := []string{
		harness.FastTimers,
		"directory:\n  reconcile_interval: 1s",
		fmt.Sprintf("broker:\n  listen: %q\n  advertise: %q", listen, listen),
	}
	if public {
		parts = append(parts, "broker:\n  public: true")
	}
	return harness.ConfigYAML(parts...)
}

// dxStartBroker starts a Broker with the given environment and fails the test
// when it does not come up.
func dxStartBroker(t *testing.T, node *harness.Node, config string, env map[string]string) {
	t.Helper()
	start := node.StartBroker(t, harness.BrokerSpec{ConfigYAML: config, Env: env})
	if !start.Running {
		t.Fatalf("the Broker did not start: exit code %d: %s",
			start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
}

// dxPair makes the dialer connect to the acceptor's listen address and returns
// the acceptor's fingerprint, which the dialer learns from the pairing result.
func dxPair(t *testing.T, acceptor, dialer *harness.Session, listen string) string {
	t.Helper()
	key := adCreateKey(t, acceptor)
	r := adConnect(t, dialer, adShownAddress(t, key.Addresses, listen), key.Key)
	r.MustOK(t)
	out, err := harness.Decode[mcpserver.ConnectBrokerOut](string(r.Structured))
	if err != nil {
		t.Fatalf("decoding connect_broker: %v", err)
	}
	if out.Fingerprint == "" {
		t.Fatalf("connect_broker returned no fingerprint")
	}
	return out.Fingerprint
}

// dxListBrokers returns the Brokers known to node.
func dxListBrokers(t *testing.T, node *harness.Node) *localpb.ListBrokersResponse {
	t.Helper()
	resp, detail := node.Oracle().ListBrokers(t)
	if detail != nil {
		t.Fatalf("listing Brokers: %s", detail.GetMessage())
	}
	return resp
}

// dxFindBroker returns the entry whose fingerprint is fp, written as lowercase
// hexadecimal.
func dxFindBroker(resp *localpb.ListBrokersResponse, fp string) (*localpb.BrokerInfo, bool) {
	for _, broker := range resp.GetBrokers() {
		if hex.EncodeToString(broker.GetFingerprint()) == fp {
			return broker, true
		}
	}
	return nil, false
}

// dxWaitCard waits until node lists fp as a connectable non-peer whose
// advertised addresses are exactly want, in order.
func dxWaitCard(t *testing.T, node *harness.Node, fp string, want []string, bound time.Duration, notes ...string) {
	t.Helper()
	what := "the card of " + fp + " shows " + strings.Join(want, ", ")
	if len(notes) > 0 {
		what += " " + notes[0]
	}
	harness.Eventually(t, bound, what, func() (bool, string) {
		resp := dxListBrokers(t, node)
		broker, ok := dxFindBroker(resp, fp)
		if !ok {
			return false, "the card is not listed"
		}
		if broker.GetIsPeer() {
			return false, "the card is listed as a peer"
		}
		if got := broker.GetStatus(); got != localpb.BrokerStatus_BROKER_STATUS_CONNECTABLE {
			return false, "status is " + got.String()
		}
		if got := adPeerAddresses(broker); !adSameStrings(got, want) {
			return false, fmt.Sprintf("addresses are %v", got)
		}
		return true, ""
	})
}

// dxListTopics calls list_topics on session and returns the decoded result, or
// a state string describing the failure.
func dxListTopics(t *testing.T, session *harness.Session) (mcpserver.ListTopicsOut, string) {
	t.Helper()
	r := session.Call(t, "list_topics", map[string]any{})
	if r.IsError {
		return mcpserver.ListTopicsOut{}, r.Text
	}
	out, err := harness.Decode[mcpserver.ListTopicsOut](string(r.Structured))
	if err != nil {
		return mcpserver.ListTopicsOut{}, err.Error()
	}
	return out, ""
}

// dxHasPublicTopic reports whether name is among the public topics.
func dxHasPublicTopic(out mcpserver.ListTopicsOut, name string) bool {
	for _, topic := range out.Public {
		if topic.Name == name {
			return true
		}
	}
	return false
}
