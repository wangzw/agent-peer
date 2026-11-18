package termcmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

func TestWriteStatusListsAgentsAndTopics(t *testing.T) {
	fingerprint := make([]byte, 32)
	for i := range fingerprint {
		fingerprint[i] = byte(i + 1)
	}

	resp := &localpb.StatusResponse{
		Agents: []*localpb.AgentStatusEntry{
			{
				AgentId: "bob",
				Address: "bob@h",
				Account: "wangzw",
				Topics:  []string{"t2"},
				Status:  localpb.AgentStatus_AGENT_STATUS_BUSY,
			},
			{
				AgentId: "alice",
				Address: "alice@h",
				Account: "wangzw",
				Topics:  []string{"t1", "t0"},
				Status:  localpb.AgentStatus_AGENT_STATUS_IDLE,
			},
		},
		Peers: []*localpb.BrokerInfo{
			{
				Alias:       "host-b",
				Fingerprint: fingerprint,
				Status:      localpb.BrokerStatus_BROKER_STATUS_CONNECTABLE,
				Role:        localpb.PeerRole_PEER_ROLE_DIALER,
				LinkState:   localpb.LinkState_LINK_STATE_BACKOFF,
				AdvertisedAddresses: []*localpb.AdvertisedAddress{
					{Kind: "direct", Addr: "10.0.0.2:17443"},
					{Kind: "relay", Addr: "10.0.0.4:17443"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteStatus(&buf, resp); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	want := "Agents (2):\n" +
		"  ADDRESS  ACCOUNT  STATUS  TOPICS  PREVIOUS NAME\n" +
		"  alice@h  wangzw   idle    t0,t1   -\n" +
		"  bob@h    wangzw   busy    t2      -\n" +
		"Peers (1):\n" +
		"  ALIAS   FINGERPRINT                                                       ROLE    STATUS       LINK     ADDRESSES                                    DETAIL\n" +
		"  host-b  0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20  dialer  connectable  backoff  direct 10.0.0.2:17443, relay 10.0.0.4:17443  -\n"
	if got := buf.String(); got != want {
		t.Errorf("WriteStatus output =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteStatusEmptySections(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStatus(&buf, &localpb.StatusResponse{}); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	if got, want := buf.String(), "Agents: none\nPeers: none\n"; got != want {
		t.Errorf("WriteStatus output = %q, want %q", got, want)
	}
}

func TestWriteStatusPeersOmitted(t *testing.T) {
	resp := &localpb.StatusResponse{
		Agents: []*localpb.AgentStatusEntry{
			{Address: "alice@h", Status: localpb.AgentStatus_AGENT_STATUS_IDLE},
		},
	}
	var buf bytes.Buffer
	if err := WriteStatus(&buf, resp); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "Agents (1):") || !strings.HasSuffix(got, "Peers: none\n") {
		t.Errorf("WriteStatus output = %q, want an Agents section followed by %q", got, "Peers: none\n")
	}
}

func TestWriteStatusFingerprintFullHex(t *testing.T) {
	fingerprint := bytes.Repeat([]byte{0xab}, 32)
	resp := &localpb.StatusResponse{
		Peers: []*localpb.BrokerInfo{{Fingerprint: fingerprint}},
	}
	var buf bytes.Buffer
	if err := WriteStatus(&buf, resp); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	want := strings.Repeat("ab", 32)
	if !strings.Contains(buf.String(), want) {
		t.Errorf("WriteStatus output = %q, want it to contain the full lowercase fingerprint %q", buf.String(), want)
	}
}

func TestWriteStatusSortsPeers(t *testing.T) {
	resp := &localpb.StatusResponse{
		Peers: []*localpb.BrokerInfo{
			{Alias: "host-z", Fingerprint: bytes.Repeat([]byte{0x02}, 32)},
			{Alias: "host-a", Fingerprint: bytes.Repeat([]byte{0x01}, 32)},
		},
	}
	var buf bytes.Buffer
	if err := WriteStatus(&buf, resp); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	got := buf.String()
	first := strings.Index(got, "host-a")
	last := strings.Index(got, "host-z")
	if first < 0 || last < 0 {
		t.Fatalf("WriteStatus output = %q, want both peers listed", got)
	}
	if first > last {
		t.Errorf("WriteStatus output = %q, want host-a before host-z", got)
	}
}

func TestWriteAuditEventFields(t *testing.T) {
	ev := &localpb.AuditEvent{
		Time:     timestamppb.New(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)),
		Event:    localpb.AuditEventKind_AUDIT_EVENT_KIND_CONFIG_SET,
		Actor:    "alice@h",
		Account:  "wangzw",
		Key:      "send.request_timeout",
		OldValue: &localpb.ConfigValue{Value: &localpb.ConfigValue_DurationValue{DurationValue: durationpb.New(30 * time.Second)}},
		NewValue: &localpb.ConfigValue{Value: &localpb.ConfigValue_DurationValue{DurationValue: durationpb.New(time.Minute)}},
	}

	var buf bytes.Buffer
	if err := WriteAuditEvent(&buf, ev); err != nil {
		t.Fatalf("WriteAuditEvent: %v", err)
	}
	want := "2026-11-18T10:00:00Z config_set actor=alice@h account=wangzw key=send.request_timeout old=30s new=1m0s\n"
	if got := buf.String(); got != want {
		t.Errorf("WriteAuditEvent output = %q, want %q", got, want)
	}
}

func TestWriteAuditEventQuotingAndCount(t *testing.T) {
	fingerprint := bytes.Repeat([]byte{0xcd}, 32)
	ev := &localpb.AuditEvent{
		Time:   timestamppb.New(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)),
		Event:  localpb.AuditEventKind_AUDIT_EVENT_KIND_PAIRING_FAILED,
		Peer:   &localpb.PeerRef{Alias: "host-b", Fingerprint: fingerprint},
		Count:  3,
		Detail: `bad key "x"`,
	}

	var buf bytes.Buffer
	if err := WriteAuditEvent(&buf, ev); err != nil {
		t.Fatalf("WriteAuditEvent: %v", err)
	}
	got := buf.String()

	peer := "peer=host-b(" + strings.Repeat("cd", 32) + ")"
	for _, want := range []string{peer, "count=3", `detail="bad key \"x\""`} {
		if !strings.Contains(got, want) {
			t.Errorf("WriteAuditEvent output = %q, want it to contain %q", got, want)
		}
	}

	ev.Count = 0
	buf.Reset()
	if err := WriteAuditEvent(&buf, ev); err != nil {
		t.Fatalf("WriteAuditEvent: %v", err)
	}
	if strings.Contains(buf.String(), "count=") {
		t.Errorf("WriteAuditEvent output = %q, want no count field when the count is zero", buf.String())
	}
}

func TestFormatConfigValueKinds(t *testing.T) {
	cases := []struct {
		name  string
		value *localpb.ConfigValue
		want  string
	}{
		{"nil", nil, ""},
		{"no oneof", &localpb.ConfigValue{}, ""},
		{"string", &localpb.ConfigValue{Value: &localpb.ConfigValue_StringValue{StringValue: "hello"}}, "hello"},
		{"bool true", &localpb.ConfigValue{Value: &localpb.ConfigValue_BoolValue{BoolValue: true}}, "true"},
		{"bool false", &localpb.ConfigValue{Value: &localpb.ConfigValue_BoolValue{BoolValue: false}}, "false"},
		{"int", &localpb.ConfigValue{Value: &localpb.ConfigValue_IntValue{IntValue: -42}}, "-42"},
		{"duration", &localpb.ConfigValue{Value: &localpb.ConfigValue_DurationValue{DurationValue: durationpb.New(90 * time.Second)}}, "1m30s"},
		{"string list", &localpb.ConfigValue{Value: &localpb.ConfigValue_StringListValue{StringListValue: &localpb.StringList{Values: []string{"a", "b"}}}}, "[a, b]"},
		{"secret set", &localpb.ConfigValue{Value: &localpb.ConfigValue_SecretIsSet{SecretIsSet: true}}, "<set>"},
		{"secret unset", &localpb.ConfigValue{Value: &localpb.ConfigValue_SecretIsSet{SecretIsSet: false}}, "<unset>"},
	}
	for _, tc := range cases {
		if got := FormatConfigValue(tc.value); got != tc.want {
			t.Errorf("FormatConfigValue(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
