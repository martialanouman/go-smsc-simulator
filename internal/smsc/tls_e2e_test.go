package smsc_test

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

// tlsHealthyConfig is a healthy virtual SMSC with TLS enabled and no supplied cert, so
// the engine auto-generates a self-signed one at boot.
func tlsHealthyConfig(name string) config.VirtualSMSCConfig {
	cfg := healthyConfig(name)
	cfg.TLS = config.TLSConfig{Enabled: true}
	return cfg
}

// insecureTLS is the loopback client config: the server's self-signed cert is in no
// system pool, so skip chain verification. This is a test peer on 127.0.0.1, not a trust
// decision about a remote host.
func insecureTLS() *tls.Config {
	//nolint:gosec // G402: loopback test peer; the server's self-signed cert is in no system pool.
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
}

// TestE2E_TLSBindSucceedsWithGeneratedCert: a TLS client binds and submits over a
// tls-enabled listener whose cert was auto-generated at boot (S6/T2 acceptance).
func TestE2E_TLSBindSucceedsWithGeneratedCert(t *testing.T) {
	t.Parallel()

	h := startWith(t, tlsHealthyConfig("carrier-tls"))
	client := smpptest.DialTLS(t, h.smppAddr, insecureTLS())

	if resp := client.BindTransceiver(testSystemID, testPassword); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("TLS bind status = %d, want ESME_ROK", resp.CommandStatus)
	}
	if got := client.SubmitStatus("33600000000", "33611111111", "over tls"); got != smpp.StatusROK {
		t.Fatalf("TLS submit status = %d, want ESME_ROK", got)
	}
}

// TestE2E_TLSListenerRejectsPlainClient: a plain-TCP client against a tls-enabled listener
// is dropped — its SMPP bytes are not a valid TLS handshake, so the server aborts and
// closes (S6/T2 acceptance: non-TLS bind refused when tls.enabled).
func TestE2E_TLSListenerRejectsPlainClient(t *testing.T) {
	t.Parallel()

	h := startWith(t, tlsHealthyConfig("carrier-tls-plain"))
	client := smpptest.Dial(t, h.smppAddr)

	// Writing SMPP bytes where the server expects a TLS ClientHello makes the handshake
	// fail; the server then closes the link rather than answering.
	client.SubmitAsync("33600000000", "33611111111", "plain")
	client.ExpectClosed()
}

// TestE2E_PlainListenerRejectsTLSClient: the inverse — a TLS handshake against a plain
// (tls.enabled=false) listener must fail, since the server never speaks TLS.
func TestE2E_PlainListenerRejectsTLSClient(t *testing.T) {
	t.Parallel()

	h := start(t, "carrier-plain") // healthyConfig: TLS disabled

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", h.smppAddr, insecureTLS())
	if err == nil {
		_ = conn.Close()
		t.Fatal("TLS handshake succeeded against a plain listener, want failure")
	}
}

// TestNew_MalformedTLSCertFailsBeforeListening pins invariant (b) for TLS: a present but
// malformed cert (which config validation only existence-checks) must fail in smsc.New
// before any listener is opened, not after some ports are already bound.
func TestNew_MalformedTLSCertFailsBeforeListening(t *testing.T) {
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

	cfg := tlsHealthyConfig("carrier-bad-cert")
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	engine, err := smsc.New([]config.VirtualSMSCConfig{cfg}, nil, logger)
	if err == nil {
		_ = engine.Shutdown(t.Context())
		t.Fatal("smsc.New accepted a malformed TLS cert, want a fail-fast error before listening")
	}
}
