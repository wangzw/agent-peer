//go:build e2e

package fakerec

import (
	"bytes"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/directory/directorypb"
	"github.com/wangzw/agent-peer/internal/pairing"
	"google.golang.org/protobuf/proto"
)

func bodyOf(t *testing.T, rec *directorypb.Record) *directorypb.RecordBody {
	t.Helper()
	body := &directorypb.RecordBody{}
	if err := proto.Unmarshal(rec.Raw, body); err != nil {
		t.Fatalf("unmarshal record body: %v", err)
	}
	return body
}

func TestBuildRecordVerifies(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	specs := []Spec{
		{
			Seed:      "card-seed",
			Kind:      "card",
			Alias:     "alpha",
			Addresses: []Addr{{Kind: "direct", Addr: "127.0.0.1:17443"}},
			Version:   1,
			ExpiresIn: 168 * time.Hour,
		},
		{
			Seed:        "topic-seed",
			Kind:        "topic",
			Topic:       "infra-help",
			Description: "ask infra",
			Version:     2,
			ExpiresIn:   168 * time.Hour,
		},
	}
	for _, s := range specs {
		t.Run(s.Kind, func(t *testing.T) {
			rec, err := Build(s, now)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			body := bodyOf(t, rec)
			if !pairing.Verify(body.PublicKey, rec.Raw, rec.Signature) {
				t.Fatal("the untampered record did not verify")
			}
			want := PublisherOf(s.Seed)
			if !bytes.Equal(body.Publisher, want[:]) {
				t.Fatalf("publisher = %x, want %x", body.Publisher, want[:])
			}
		})
	}
}

func TestTamperAddressesBreaksSignature(t *testing.T) {
	rec, err := Build(Spec{
		Seed:      "card-seed",
		Kind:      "card",
		Alias:     "alpha",
		Addresses: []Addr{{Kind: "direct", Addr: "127.0.0.1:17443"}},
		Version:   1,
		ExpiresIn: time.Hour,
		Tamper:    "addresses",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body := bodyOf(t, rec)
	if pairing.Verify(body.PublicKey, rec.Raw, rec.Signature) {
		t.Fatal("a record with tampered addresses still verified")
	}
}

func TestTamperPublisherMismatch(t *testing.T) {
	rec, err := Build(Spec{
		Seed:      "card-seed",
		Kind:      "card",
		Alias:     "alpha",
		Version:   1,
		ExpiresIn: time.Hour,
		Tamper:    "publisher",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body := bodyOf(t, rec)
	if !pairing.Verify(body.PublicKey, rec.Raw, rec.Signature) {
		t.Fatal("the impostor record should carry a valid signature")
	}
	fp := pairing.FingerprintOf(body.PublicKey)
	if bytes.Equal(body.Publisher, fp[:]) {
		t.Fatal("the claimed publisher equals the real publisher fingerprint")
	}
}

func TestSeedDeterministic(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	s := Spec{Seed: "same-seed", Kind: "card", Alias: "alpha", Version: 1, ExpiresIn: time.Hour}
	first, err := Build(s, now)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	second, err := Build(s, now)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	firstBody := bodyOf(t, first)
	secondBody := bodyOf(t, second)
	if !bytes.Equal(firstBody.PublicKey, secondBody.PublicKey) {
		t.Fatalf("public keys differ: %x vs %x", firstBody.PublicKey, secondBody.PublicKey)
	}
	firstPublisher := PublisherOf("same-seed")
	secondPublisher := PublisherOf("same-seed")
	if firstPublisher != secondPublisher {
		t.Fatal("PublisherOf is not deterministic")
	}
}
