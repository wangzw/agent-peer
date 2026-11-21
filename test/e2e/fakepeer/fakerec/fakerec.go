//go:build e2e

// Package fakerec builds signed directory records for the end-to-end tests.
//
// The records are shaped after the rules the directory applies when it parses
// and verifies a Record: the publisher fingerprint and the raw public key live
// in the body, the signature covers the deterministic serialization of the
// body, and the payload carries either a broker card or a topic announcement.
// Build can also produce deliberately broken records for the hostile-source
// tests.
package fakerec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/wangzw/peers/internal/directory/directorypb"
	"github.com/wangzw/peers/internal/pairing"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Addr is an advertised address on a broker card.
type Addr struct {
	Kind string
	Addr string
}

// Spec describes one record to build.
type Spec struct {
	Seed        string // publisher key: ed25519.NewKeyFromSeed(sha256(Seed))
	Kind        string // "card" or "topic"
	Alias       string // card only
	Addresses   []Addr // card only
	Topic       string // topic only
	Description string // topic only
	Version     uint64
	ExpiresIn   time.Duration // expires = now + ExpiresIn
	Tamper      string        // "", "addresses" or "publisher"
}

// publisherKey derives the publisher key pair from a seed.
func publisherKey(seed string) (ed25519.PrivateKey, ed25519.PublicKey) {
	sum := sha256.Sum256([]byte(seed))
	priv := ed25519.NewKeyFromSeed(sum[:])
	pub := priv.Public().(ed25519.PublicKey)
	return priv, pub
}

// PublisherOf returns the fingerprint of the publisher key derived from seed.
func PublisherOf(seed string) pairing.Fingerprint {
	_, pub := publisherKey(seed)
	return pairing.FingerprintOf(pub)
}

// Build constructs a signed directory record from s. now fixes the expiration
// time together with s.ExpiresIn; the caller passes the clock the receiving
// broker observes.
func Build(s Spec, now time.Time) (*directorypb.Record, error) {
	priv, pub := publisherKey(s.Seed)
	fp := pairing.FingerprintOf(pub)
	if fp == (pairing.Fingerprint{}) {
		return nil, fmt.Errorf("fakerec: derive publisher fingerprint from seed %q", s.Seed)
	}

	body := &directorypb.RecordBody{
		Publisher: append([]byte(nil), fp[:]...),
		PublicKey: append([]byte(nil), pub...),
		Version:   s.Version,
		Expires:   timestamppb.New(now.Add(s.ExpiresIn)),
	}

	switch s.Kind {
	case "card":
		body.Kind = directorypb.RecordKind_RECORD_KIND_BROKER_CARD
		body.Object = ""
		addrs := make([]*directorypb.AdvertisedAddress, 0, len(s.Addresses))
		for _, a := range s.Addresses {
			addrs = append(addrs, &directorypb.AdvertisedAddress{Kind: a.Kind, Addr: a.Addr})
		}
		body.Payload = &directorypb.RecordBody_BrokerCard{
			BrokerCard: &directorypb.BrokerCard{Alias: s.Alias, Addresses: addrs},
		}
	case "topic":
		body.Kind = directorypb.RecordKind_RECORD_KIND_TOPIC_ANNOUNCEMENT
		body.Object = s.Topic
		body.Payload = &directorypb.RecordBody_TopicAnnouncement{
			TopicAnnouncement: &directorypb.TopicAnnouncement{Name: s.Topic, Description: s.Description},
		}
	default:
		return nil, fmt.Errorf("fakerec: unknown record kind %q", s.Kind)
	}

	switch s.Tamper {
	case "":
	case "publisher":
		// Sign over a body whose publisher does not match the public key, so the
		// signature verifies while the claimed publisher is someone else.
		other := sha256.Sum256([]byte("other:" + s.Seed))
		body.Publisher = append([]byte(nil), other[:]...)
	case "addresses":
		if s.Kind != "card" {
			return nil, fmt.Errorf("fakerec: the addresses tamper needs a card record, got %q", s.Kind)
		}
	default:
		return nil, fmt.Errorf("fakerec: unknown tamper %q", s.Tamper)
	}

	opts := proto.MarshalOptions{Deterministic: true}
	raw, err := opts.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("fakerec: marshal record body: %w", err)
	}
	sig := ed25519.Sign(priv, raw)

	if s.Tamper == "addresses" {
		// Replace the advertised addresses after signing while keeping the old
		// signature, so verification fails over the new raw bytes.
		card := body.GetBrokerCard()
		card.Addresses = []*directorypb.AdvertisedAddress{
			{Kind: "direct", Addr: "203.0.113.99:1"},
		}
		raw, err = opts.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("fakerec: marshal tampered record body: %w", err)
		}
	}

	return &directorypb.Record{Raw: raw, Signature: sig}, nil
}
