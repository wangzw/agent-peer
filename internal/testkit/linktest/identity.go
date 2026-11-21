package linktest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/wangzw/peers/internal/pairing"
)

// Identity is a self-signed Ed25519 identity for one fake Broker. Its
// fingerprint is derived from the certificate's SubjectPublicKeyInfo exactly
// as pairing derives one, so the fingerprint checks link performs behave the
// same against a fake as against a real identity.
type Identity struct {
	Cert        tls.Certificate
	Leaf        *x509.Certificate
	PublicKey   ed25519.PublicKey
	Fingerprint pairing.Fingerprint
}

// NewIdentity generates a fresh Ed25519 key and a self-signed certificate valid
// for one year. It fails the test if either generation step fails.
func NewIdentity(t testing.TB) Identity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("linktest: generate Ed25519 key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("linktest: generate certificate serial: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "linktest"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatalf("linktest: create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("linktest: parse certificate: %v", err)
	}
	return Identity{
		Cert: tls.Certificate{
			Certificate: [][]byte{der},
			PrivateKey:  priv,
			Leaf:        leaf,
		},
		Leaf:        leaf,
		PublicKey:   pub,
		Fingerprint: pairing.FingerprintOf(leaf.RawSubjectPublicKeyInfo),
	}
}

// TLSConfig returns a fresh TLS configuration presenting this identity. It
// mirrors pairing's own TLSConfig: TLS 1.3 or later, request the peer's
// certificate, and skip certificate-chain verification because identity comes
// from the fingerprint check instead.
func (id Identity) TLSConfig() *tls.Config {
	// InsecureSkipVerify is deliberate here, as in pairing.TLSConfig: the
	// certificate chain proves nothing, and link verifies the peer by
	// fingerprint after the handshake.
	return &tls.Config{
		Certificates:       []tls.Certificate{id.Cert},
		MinVersion:         tls.VersionTLS13,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true,
	}
}
