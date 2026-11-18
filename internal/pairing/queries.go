package pairing

import "crypto/tls"

// Fingerprint returns this Broker's certificate fingerprint. It returns the zero
// Fingerprint when the instance has no identity.
func (p *Pairing) Fingerprint() Fingerprint {
	if p == nil || p.identity == nil {
		return Fingerprint{}
	}
	return p.identity.fingerprint()
}

// TLSConfig returns a fresh TLS configuration that presents this Broker's
// certificate. The caller may modify the copy without affecting the next call.
// It returns nil when the instance has no identity.
//
// InsecureSkipVerify is deliberate: TLS proves possession of the certificate's
// private key, while identity comes from the fingerprint check and the pairing
// handshake. ClientAuth requests the peer's certificate so link can read it.
func (p *Pairing) TLSConfig() *tls.Config {
	if p == nil || p.identity == nil {
		return nil
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cloneTLSCertificate(p.identity.cert)},
		MinVersion:         tls.VersionTLS13,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true,
	}
}

// cloneTLSCertificate copies the certificate chain's byte slices. Copying the
// tls.Certificate struct alone would share the DER buffers with the identity, so
// a caller that mutates the returned chain could corrupt later TLSConfig calls
// and the identity itself.
func cloneTLSCertificate(cert tls.Certificate) tls.Certificate {
	out := cert
	if cert.Certificate != nil {
		out.Certificate = make([][]byte, len(cert.Certificate))
		for i, der := range cert.Certificate {
			out.Certificate[i] = append([]byte(nil), der...)
		}
	}
	return out
}

// PublicKey returns a copy of this Broker's raw 32-byte Ed25519 public key.
// It returns nil when the instance has no identity.
func (p *Pairing) PublicKey() []byte {
	if p == nil || p.identity == nil {
		return nil
	}
	return p.identity.publicKey()
}

// Sign signs data with this Broker's Ed25519 private key. It returns nil when
// the instance has no identity.
func (p *Pairing) Sign(data []byte) []byte {
	if p == nil || p.identity == nil {
		return nil
	}
	return p.identity.sign(data)
}
