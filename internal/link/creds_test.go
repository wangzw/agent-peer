package link

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
)

// TestIdentityFromStateRejectsNonEd25519 checks that a peer certificate with a
// key other than Ed25519 is refused: the fingerprint must always name an
// Ed25519 key taken from the SubjectPublicKeyInfo.
func TestIdentityFromStateRejectsNonEd25519(t *testing.T) {
	st := tls.ConnectionState{
		Version:          tls.VersionTLS13,
		PeerCertificates: []*x509.Certificate{{PublicKey: &rsa.PublicKey{}}},
	}
	_, err := identityFromState(st, "127.0.0.1:1")
	if err == nil {
		t.Fatal("identityFromState accepted a non-Ed25519 certificate key")
	}
	if !strings.Contains(err.Error(), "not Ed25519") {
		t.Fatalf("error = %v, want it to name the Ed25519 requirement", err)
	}
}
