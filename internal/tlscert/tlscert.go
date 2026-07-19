// Package tlscert provides the boot-time TLS material for a virtual SMSC listener:
// either a certificate loaded from configured PEM files, or an in-memory self-signed
// certificate generated on the spot. Generation happens once at boot, never on a
// per-PDU path, so its use of crypto/rand and the wall clock is off the deterministic
// path — the CLAUDE.md wall-clock rule governs scheduling, not boot-time setup.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// certValidity is how long a generated self-signed cert stays valid — a year, ample
// for a test/CI cert. This cert is never browser-facing (only the in-process SMPP test
// client and a real gateway connector dial it), so the 398-day browser cap is moot.
const certValidity = 365 * 24 * time.Hour

// serialBits bounds the random certificate serial number (RFC 5280 allows up to 20 octets).
const serialBits = 128

// SelfSigned returns a fresh in-memory ECDSA P-256 self-signed certificate for a TLS
// listener. Its SANs cover localhost, 127.0.0.1 and ::1 so a loopback client can verify
// the name; it is marked as its own CA so a client that pins it via RootCAs also
// validates. Boot-time only — never called on the deterministic per-PDU path.
func SelfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate ecdsa key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "go-smsc-simulator self-signed"},
		NotBefore:             now.Add(-1 * time.Hour), // tolerate small client clock skew
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		// Loopback-only SANs by design: a hostname-verifying client can reach this cert
		// only over loopback. A non-loopback client (a docker-compose service dialed by
		// name) must be given a cert_file/key_file whose SANs cover that name (see
		// config.TLSConfig). Kept intentionally minimal rather than guessing deploy names.
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse generated certificate: %w", err)
	}
	// Assemble the tls.Certificate directly from the DER and the in-hand key — no PEM
	// round-trip. Leaf is set so the TLS stack skips re-parsing on first handshake.
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

// LoadOrGenerate returns the TLS certificate for a virtual SMSC: the configured PEM key
// pair when cert_file is set, otherwise a fresh self-signed cert. Config validation has
// already ensured cert_file/key_file are set together and exist, so a load failure here
// is a genuine parse error rather than a missing file.
func LoadOrGenerate(cfg config.TLSConfig) (tls.Certificate, error) {
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load tls key pair: %w", err)
		}
		return cert, nil
	}
	return SelfSigned()
}
