package topic

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/topic/topicpb"
)

// TestValidateName checks the rules for a legal topic name.
// REQ-topics-001: a topic is joined by name, so the name must be legal first.
func TestValidateName(t *testing.T) {
	valid := []string{
		"release",
		"github.com/org/repo",
		"a b",
		"名字",
		strings.Repeat("a", 256),
		strings.Repeat("名", 85), // 255 bytes
		"Release",
	}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("a", 257),
		strings.Repeat("名", 86), // 258 bytes
		"a\x00b",
		"a\nb",
		"\x7f",
		"a\u0085b",
		"\xff",
	}
	for _, name := range invalid {
		if err := ValidateName(name); !errors.Is(err, ErrInvalidTopic) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidTopic", name, err)
		}
	}
}

func TestDigestOrderAndValue(t *testing.T) {
	// SHA-256("abc") proves salt first, name second.
	if got, want := digest([]byte("ab"), "c"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; hex.EncodeToString(got[:]) != want {
		t.Errorf("digest(\"ab\", \"c\") = %s, want %s", hex.EncodeToString(got[:]), want)
	}
	if digest([]byte("s1"), "t") == digest([]byte("s2"), "t") {
		t.Error("digest ignores the salt")
	}

	// A digest must not write into the caller's salt, not even into its spare
	// capacity.
	salt := make([]byte, 3, 16)
	copy(salt, "s1x")
	before := append([]byte(nil), salt[:cap(salt)]...)
	digest(salt, "t")
	if !bytes.Equal(salt[:cap(salt)], before) {
		t.Errorf("digest modified the caller's salt: %q -> %q", before, salt[:cap(salt)])
	}
}

func TestNewPeerStateCopiesSalt(t *testing.T) {
	salt := []byte("s1")
	ps := newPeerState(salt, []string{"t1", "t2"})
	salt[0] = 'X'
	if string(ps.salt) != "s1" {
		t.Errorf("ps.salt = %q, want %q (the salt was not copied)", ps.salt, "s1")
	}

	want := digest([]byte("s1"), "t1")
	if got := ps.digest["t1"]; got != want {
		t.Errorf("ps.digest[\"t1\"] = %x, want %x", got, want)
	}
	if got := ps.name[want]; got != "t1" {
		t.Errorf("ps.name[digest(\"t1\")] = %q, want %q", got, "t1")
	}

	if ps.remote == nil || len(ps.remote) != 0 {
		t.Errorf("ps.remote = %v, want an allocated empty map", ps.remote)
	}
	if ps.shared == nil || len(ps.shared) != 0 {
		t.Errorf("ps.shared = %v, want an allocated empty map", ps.shared)
	}
	if ps.rosters == nil || len(ps.rosters) != 0 {
		t.Errorf("ps.rosters = %v, want an allocated empty map", ps.rosters)
	}
}

// sendFailTransport is the smallest Transport implementation: Send always
// fails, the other methods do nothing.
type sendFailTransport struct {
	err error
}

func (s *sendFailTransport) Send(link.Fingerprint, link.Frame) error { return s.err }

func (s *sendFailTransport) HandleState(link.FrameKind, func(link.Fingerprint, link.Frame)) {}

func (s *sendFailTransport) Subscribe(func(link.Fingerprint, []byte), func(link.Fingerprint)) {
}

func TestSendLockedLogsFailure(t *testing.T) {
	var buf bytes.Buffer
	r := &Registry{
		t:   &sendFailTransport{err: errors.New("link is down")},
		log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	r.sendLocked(link.Fingerprint{}, topicpb.KindTopicDigests, &topicpb.TopicDigests{})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d log records, want 1: %q", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("log record is not JSON: %v", err)
	}
	if got := rec["msg"]; got != "topic.send_failed" {
		t.Errorf("log msg = %v, want topic.send_failed", got)
	}
	if _, ok := rec["kind"]; !ok {
		t.Errorf("log record has no kind field: %s", lines[0])
	}
}
