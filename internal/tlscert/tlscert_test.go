package tlscert

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// TestSelfSigned_LoopbackServerCert asserts the generated cert is a usable loopback TLS
// server cert: an ECDSA key, SANs covering localhost/127.0.0.1/::1 (so a loopback client
// can verify the name), server-auth EKU, and a currently-valid window.
func TestSelfSigned_LoopbackServerCert(t *testing.T) {
	t.Parallel()

	cert, err := SelfSigned()
	if err != nil {
		t.Fatalf("SelfSigned: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("SelfSigned returned no certificate DER")
	}
	if _, ok := cert.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Errorf("private key type = %T, want *ecdsa.PrivateKey", cert.PrivateKey)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Errorf("VerifyHostname(%q) = %v, want nil", host, err)
		}
	}

	if err := leaf.VerifyHostname("evil.example.com"); err == nil {
		t.Error("VerifyHostname(evil.example.com) = nil, want an error (SAN must be bounded)")
	}

	var serverAuth bool
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			serverAuth = true
		}
	}
	if !serverAuth {
		t.Error("leaf missing ExtKeyUsageServerAuth")
	}

	now := time.Now()
	if !leaf.NotBefore.Before(now) || !leaf.NotAfter.After(now) {
		t.Errorf("validity [%v,%v] does not contain now %v", leaf.NotBefore, leaf.NotAfter, now)
	}
}

// TestSelfSigned_Unique confirms each call mints a fresh cert (distinct serial), so two
// virtual SMSCs never share key material.
func TestSelfSigned_Unique(t *testing.T) {
	t.Parallel()

	a, err := SelfSigned()
	if err != nil {
		t.Fatalf("SelfSigned #1: %v", err)
	}
	b, err := SelfSigned()
	if err != nil {
		t.Fatalf("SelfSigned #2: %v", err)
	}
	la, _ := x509.ParseCertificate(a.Certificate[0])
	lb, _ := x509.ParseCertificate(b.Certificate[0])
	if la.SerialNumber.Cmp(lb.SerialNumber) == 0 {
		t.Error("two SelfSigned certs share a serial number")
	}
}

// TestLoadOrGenerate_GeneratesWhenNoCert covers the auto-generation branch: an enabled
// tls block with no cert_file yields a self-signed cert.
func TestLoadOrGenerate_GeneratesWhenNoCert(t *testing.T) {
	t.Parallel()

	cert, err := LoadOrGenerate(config.TLSConfig{Enabled: true})
	if err != nil {
		t.Fatalf("LoadOrGenerate (auto-gen): %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("auto-gen returned no certificate")
	}
}

// TestLoadOrGenerate_LoadsSuppliedCert covers the load branch: a written PEM key pair is
// loaded from disk rather than generated.
func TestLoadOrGenerate_LoadsSuppliedCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeKeyPair(t, certPath, keyPath)

	cert, err := LoadOrGenerate(config.TLSConfig{Enabled: true, CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatalf("LoadOrGenerate (supplied): %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("supplied load returned no certificate")
	}
}

// TestLoadOrGenerate_RejectsGarbageCert covers the parse-error branch (config validation
// only checks existence, not that the file parses).
func TestLoadOrGenerate_RejectsGarbageCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrGenerate(config.TLSConfig{Enabled: true, CertFile: certPath, KeyFile: keyPath}); err == nil {
		t.Fatal("LoadOrGenerate accepted a garbage cert, want a parse error")
	}
}

// writeKeyPair generates a self-signed cert and writes its PEM cert/key to the given
// paths, so the load branch can be exercised against real files.
func writeKeyPair(t *testing.T, certPath, keyPath string) {
	t.Helper()

	cert, err := SelfSigned()
	if err != nil {
		t.Fatalf("SelfSigned for fixture: %v", err)
	}
	certPEM, keyPEM := encodePEM(t, cert)
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

// encodePEM re-encodes a tls.Certificate's leaf and ECDSA key back to PEM bytes.
func encodePEM(t *testing.T, cert tls.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T, want *ecdsa.PrivateKey", cert.PrivateKey)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal ec key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return certPEM, keyPEM
}
