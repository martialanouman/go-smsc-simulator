package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// freePort asks the OS for a port, then releases it. There is an inherent race
// between releasing and rebinding, but these tests need to name a port inside a
// YAML fixture *before* run boots, which rules out passing port 0.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}

// TestVersionLine pins the --version output format. The value is "dev" under
// `go test` (no ldflags), so this asserts the wiring — the prefix and the
// injected symbol — rather than any specific release number. The real stamped
// value is verified by hand via `make build && ./bin/smsc-simulator --version`.
func TestVersionLine(t *testing.T) {
	t.Parallel()

	if got, want := versionLine(), "smsc-simulator "+version; got != want {
		t.Errorf("versionLine() = %q, want %q", got, want)
	}
	if version == "" {
		t.Error("version is empty; the -ldflags -X main.version target would be silently ignored")
	}
}

func TestRun_NoConfigFlag(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), "")
	if !errors.Is(err, config.ErrNoConfigPath) {
		t.Fatalf("run() with no --config = %v, want it to wrap %v", err, config.ErrNoConfigPath)
	}
}

func TestRun_UnreadableConfig(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), filepath.Join(t.TempDir(), "absent.yml"))
	if err == nil {
		t.Fatal("run() with a missing config = nil, want a failure")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("run() error = %v, want it to wrap %v", err, os.ErrNotExist)
	}
}

// TestRun_InvalidConfigOpensNoPort is invariant (b): the config is fully validated
// *before* any port is opened (plan §0.5).
//
// "Before" is an ordering property, so asserting on run's return value alone
// would prove nothing. The fixture instead names a real, free port and is also
// syntactically broken: run must fail, and the port must still be closed
// afterwards. Move config.Load below the listener setup and this test fails.
func TestRun_InvalidConfigOpensNoPort(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	path := writeConfig(t, fmt.Sprintf(
		"observability:\n  http_port: %d\n   broken indentation makes this invalid YAML\n", port))

	if err := run(context.Background(), path); err == nil {
		t.Fatal("run() with an invalid config = nil, want a failure")
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("port %d accepted a connection after an invalid config: a listener was opened before validation", port)
	}
}

// TestRun_BusinessInvalidConfigOpensNoPort extends invariant (b) to the new S1
// validation phase: the config here is syntactically valid YAML but breaks a
// business rule (duplicate virtual-SMSC port). run must fail and the observability
// port — named as a real free port — must still be closed afterwards. This proves
// validate() runs above the boot gate, not merely the parser.
func TestRun_BusinessInvalidConfigOpensNoPort(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	path := writeConfig(t, fmt.Sprintf(`observability:
  http_port: %d
virtual_smscs:
  - name: carrier-a
    port: 2775
    bind_credentials: { system_id: c1, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 42
    pdu_buffer_size: 10000
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
  - name: carrier-b
    port: 2775
    bind_credentials: { system_id: c2, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 43
    pdu_buffer_size: 10000
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
`, port))

	err := run(context.Background(), path)
	if err == nil {
		t.Fatal("run() with a business-invalid config = nil, want a failure")
	}
	if !errors.Is(err, config.ErrDuplicatePort) {
		t.Errorf("run() error = %v, want it to wrap %v", err, config.ErrDuplicatePort)
	}

	conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("port %d accepted a connection after an invalid config: a listener was opened before validation", port)
	}
}

// TestRun_GracefulShutdown exercises the real shutdown path in-process.
//
// signal.NotifyContext derives from the context run is given, so cancelling the
// parent walks exactly the same code as a SIGTERM would, without the flakiness
// of spawning a process and signalling it.
func TestRun_GracefulShutdown(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	path := writeConfig(t, fmt.Sprintf("observability:\n  http_port: %d\n", port))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, path) }()

	waitForHealth(t, port)

	cancel() // stands in for SIGTERM

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run() = %v, want a clean shutdown", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("run() did not return after cancellation, want a graceful shutdown")
	}

	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
		_ = conn.Close()
		t.Errorf("port %d still accepts connections after shutdown, want the listener closed", port)
	}
}

// TestRun_NoObservabilityBlockIsBlackBox pins spec §5.2: omitting the block must
// start no HTTP server at all, while the process still runs and stops cleanly.
func TestRun_NoObservabilityBlockIsBlackBox(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	path := writeConfig(t, "{}\n")

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, path) }()

	// Nothing to poll for readiness here — that is the point. Give run a moment
	// to reach its wait state, then confirm no surface came up.
	time.Sleep(200 * time.Millisecond)

	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
		_ = conn.Close()
		t.Errorf("port %d is listening, want no HTTP surface when the observability block is omitted", port)
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run() = %v, want a clean shutdown", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("run() did not return after cancellation")
	}
}

func waitForHealth(t *testing.T, port int) {
	t.Helper()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("build readiness request: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("GET %s never returned 200 before the deadline", url)
}
