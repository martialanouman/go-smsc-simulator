package observability_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/martialanouman/go-smsc-simulator/internal/observability"
)

// TestServer_MetricsEndpoint checks GET /metrics exposes the wired registry and, being a
// GET-only route, still 405s a mutating verb — scraping never changes state (invariant c).
func TestServer_MetricsEndpoint(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	probe := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "smsc_test_probe_total",
		Help: "A probe counter to assert /metrics exposes the wired registry.",
	})
	reg.MustRegister(probe)
	probe.Inc()

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	srv, err := observability.NewServer(0, logger, nil, reg)
	if err != nil {
		t.Fatalf("NewServer() = %v, want no error", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-serveErr
	})
	base := "http://" + srv.Addr().String()

	resp := get(t, base+"/metrics")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	if !strings.Contains(string(body), "smsc_test_probe_total") {
		t.Errorf("/metrics body does not expose the registered probe:\n%s", body)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/metrics", nil)
	if err != nil {
		t.Fatalf("build POST /metrics: %v", err)
	}
	pr, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("POST /metrics: %v", err)
	}
	defer func() { _ = pr.Body.Close() }()
	if pr.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics status = %d, want 405 (read-only surface)", pr.StatusCode)
	}
}
