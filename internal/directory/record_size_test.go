package directory

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/directory/directorypb"
)

// oversizedRecord builds and signs an announcement whose encoded raw exceeds
// the record size limit. It signs the wire form directly instead of going
// through signRecord, so the record can be handed to the receiving path.
func oversizedRecord(t *testing.T, seed byte) *directorypb.Record {
	t.Helper()
	pub, priv := recTestKey(seed)
	body := recTestBody(pub, recKindTopic, "news", 1, recTestExpiry(),
		&directorypb.TopicAnnouncement{Name: "news", Description: strings.Repeat("d", 66*1024)})
	return recTestWire(t, body, priv)
}

// TestParseRejectsOversizedRaw checks that a record whose raw bytes exceed 64
// KiB is rejected as malformed before it can be stored or forwarded.
func TestParseRejectsOversizedRaw(t *testing.T) {
	wire := oversizedRecord(t, 41)
	if len(wire.Raw) <= 64<<10 {
		t.Fatalf("test record raw is %d bytes, want more than %d", len(wire.Raw), 64<<10)
	}

	_, err := parseRecord(wire)
	if err == nil {
		t.Fatal("parseRecord accepted an oversized record")
	}
	if !errors.Is(err, errMalformed) {
		t.Fatalf("oversized record error = %v, want it to wrap errMalformed", err)
	}
}

// TestSignRecordRejectsOversizedRaw checks that the local signing path applies
// the same size limit, so the local Broker cannot issue an oversized record.
func TestSignRecordRejectsOversizedRaw(t *testing.T) {
	pub, priv := recTestKey(42)
	body := recTestBody(pub, recKindTopic, "news", 1, recTestExpiry(),
		&directorypb.TopicAnnouncement{Name: "news", Description: strings.Repeat("d", 66*1024)})

	_, err := signRecord(body, func(data []byte) []byte { return ed25519.Sign(priv, data) })
	if err == nil {
		t.Fatal("signRecord signed an oversized record")
	}
	if !errors.Is(err, errMalformed) {
		t.Fatalf("signRecord error = %v, want it to wrap errMalformed", err)
	}
}
