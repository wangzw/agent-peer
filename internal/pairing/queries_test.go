package pairing

import (
	"bytes"
	"crypto/tls"
	"testing"
)

func TestPairingFingerprintMatchesIdentity(t *testing.T) {
	p := hsNewPairing(t, newHSSettings(t), &hsTransport{}, newHSClock())

	got := p.Fingerprint()
	want := FingerprintOf(p.identity.publicKey())
	if got != want {
		t.Fatalf("Fingerprint() = %s, want %s", got, want)
	}
	if got.IsZero() {
		t.Fatal("Fingerprint() is zero for an opened instance")
	}
	if got.String() != fingerprintHex(want) {
		t.Fatalf("Fingerprint().String() = %q, want %q", got.String(), fingerprintHex(want))
	}
}

func TestZeroPairingFingerprint(t *testing.T) {
	var zero Pairing
	if got := zero.Fingerprint(); !got.IsZero() {
		t.Fatalf("zero Pairing Fingerprint() = %s, want the zero fingerprint", got)
	}
	if got := (&Pairing{}).Fingerprint(); !got.IsZero() {
		t.Fatalf("empty Pairing Fingerprint() = %s, want the zero fingerprint", got)
	}
}

func TestPairingTLSConfig(t *testing.T) {
	p := hsNewPairing(t, newHSSettings(t), &hsTransport{}, newHSClock())

	cfg := p.TLSConfig()
	if cfg == nil {
		t.Fatal("TLSConfig() = nil for an opened instance")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %#x, want TLS 1.3", cfg.MinVersion)
	}
	if cfg.ClientAuth != tls.RequestClientCert {
		t.Fatalf("ClientAuth = %v, want RequestClientCert", cfg.ClientAuth)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true: identity comes from the fingerprint check")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates has %d entries, want 1", len(cfg.Certificates))
	}
	if len(cfg.Certificates[0].Certificate) == 0 {
		t.Fatal("the presented certificate has no DER bytes")
	}
	if !bytes.Equal(cfg.Certificates[0].Certificate[0], p.identity.cert.Certificate[0]) {
		t.Fatal("TLSConfig did not present the identity certificate")
	}
	if cfg.Certificates[0].PrivateKey == nil {
		t.Fatal("the presented certificate has no private key")
	}
}

func TestPairingTLSConfigReturnsIsolatedCertificateBytes(t *testing.T) {
	p := hsNewPairing(t, newHSSettings(t), &hsTransport{}, newHSClock())
	original := append([]byte(nil), p.identity.cert.Certificate[0]...)

	first := p.TLSConfig()
	first.Certificates[0].Certificate[0][0] ^= 0xff

	second := p.TLSConfig()
	if !bytes.Equal(second.Certificates[0].Certificate[0], original) {
		t.Fatal("mutating the certificate bytes returned by TLSConfig changed the next call: " +
			"the returned certificate must be a deep copy")
	}
	if !bytes.Equal(p.identity.cert.Certificate[0], original) {
		t.Fatal("mutating the certificate bytes returned by TLSConfig changed the identity")
	}
}

func TestZeroPairingTLSConfigIsNil(t *testing.T) {
	var zero Pairing
	if cfg := zero.TLSConfig(); cfg != nil {
		t.Fatalf("zero Pairing TLSConfig() = %+v, want nil", cfg)
	}
}

func TestPairingSign(t *testing.T) {
	p := hsNewPairing(t, newHSSettings(t), &hsTransport{}, newHSClock())
	data := []byte("agent-peer pairing signature")

	sig := p.Sign(data)
	if len(sig) != 64 {
		t.Fatalf("signature length = %d, want 64", len(sig))
	}
	if !Verify(p.identity.publicKey(), data, sig) {
		t.Fatal("the signature does not verify with the identity public key")
	}
	if Verify(p.identity.publicKey(), []byte("different data"), sig) {
		t.Fatal("the signature verifies for different data")
	}
	if again := p.Sign(data); !bytes.Equal(sig, again) {
		t.Fatal("Ed25519 signing is not deterministic")
	}
}

func TestZeroPairingSignIsNil(t *testing.T) {
	var zero Pairing
	if sig := zero.Sign([]byte("data")); sig != nil {
		t.Fatalf("zero Pairing Sign() = %x, want nil", sig)
	}
}
