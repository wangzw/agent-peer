package directory

import (
	"crypto/ed25519"
	"fmt"

	"github.com/wangzw/peers/internal/pairing"
)

// identitySource is the part of *pairing.Pairing that NewIdentity uses.
type identitySource interface {
	Fingerprint() pairing.Fingerprint
	PublicKey() []byte // 32-byte raw Ed25519 key copy; nil without identity
	Sign(data []byte) []byte
}

// pairingIdentity is the production Identity backed by a pairing source.
type pairingIdentity struct {
	src identitySource
	fp  Fingerprint
	pub []byte
}

var _ Identity = (*pairingIdentity)(nil)

// NewIdentity takes the raw Ed25519 public key from p.PublicKey() and checks
// that its fingerprint equals p.Fingerprint(). It returns ErrNoIdentity
// (wrapped) when p has no identity, the key is not 32 bytes, or the
// fingerprints differ.
func NewIdentity(p *pairing.Pairing) (Identity, error) { return newIdentity(p) }

func newIdentity(src identitySource) (Identity, error) {
	if src == nil {
		return nil, fmt.Errorf("%w: the source is nil", ErrNoIdentity)
	}
	pub := src.PublicKey()
	fp := src.Fingerprint()
	if fp.IsZero() || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: the public key is missing or malformed", ErrNoIdentity)
	}
	if got := pairing.FingerprintOf(pub); got != fp {
		return nil, fmt.Errorf("%w: public key fingerprint %s does not match %s", ErrNoIdentity, got, fp)
	}
	return &pairingIdentity{
		src: src,
		fp:  fp,
		pub: append([]byte(nil), pub...),
	}, nil
}

// Fingerprint returns the fingerprint of the local certificate.
func (id *pairingIdentity) Fingerprint() Fingerprint { return id.fp }

// PublicKey returns a copy of the raw 32-byte Ed25519 public key.
func (id *pairingIdentity) PublicKey() []byte { return append([]byte(nil), id.pub...) }

// Sign returns the Ed25519 signature over data.
func (id *pairingIdentity) Sign(data []byte) []byte { return id.src.Sign(data) }
