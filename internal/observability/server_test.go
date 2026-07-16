package observability_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/observability"
)

// startServer boots the surface on an ephemeral port and returns its base URL.
// Ephemeral ports are mandatory so the suite stays parallel-safe (test strategy §2).
func startServer(t *testing.T) string {
	t.Helper()

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	srv, err := observability.NewServer(0, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want no error", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() = %v, want no error", err)
		}
		if err := <-serveErr; err != nil {
			t.Errorf("Serve() = %v, want nil on clean shutdown", err)
		}
	})

	return "http://" + srv.Addr().String()
}

// get issues a GET and fails the test if the request itself could not be made.
// The caller owns closing the body.
func get(t *testing.T, url string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func TestServer_Health(t *testing.T) {
	t.Parallel()

	resp := get(t, startServer(t)+"/health")
	defer func() { _ = resp.Body.Close() }()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET /health status = %d, want %d", got, want)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if got, want := body["status"], "ok"; got != want {
		t.Errorf("health status field = %q, want %q", got, want)
	}
}

// TestServer_RejectsMutatingVerbs is invariant (c), primed early.
//
// The invariant is formally posed at S2, but the guarantee is already structural
// at S0 thanks to ServeMux method patterns, so it is cheaper to pin it now than
// to discover at S2 that a handler drifted. Every endpoint added later must keep
// this test green.
func TestServer_RejectsMutatingVerbs(t *testing.T) {
	t.Parallel()

	baseURL := startServer(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(context.Background(), method, baseURL+"/health", nil)
			if err != nil {
				t.Fatalf("build %s request: %v", method, err)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s /health: %v", method, err)
			}
			defer func() { _ = resp.Body.Close() }()

			if got, want := resp.StatusCode, http.StatusMethodNotAllowed; got != want {
				t.Errorf("%s /health status = %d, want %d — the read-only surface must refuse mutating verbs", method, got, want)
			}
		})
	}
}

func TestServer_UnknownRouteIsNotFound(t *testing.T) {
	t.Parallel()

	resp := get(t, startServer(t)+"/nope")
	defer func() { _ = resp.Body.Close() }()

	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("GET /nope status = %d, want %d", got, want)
	}
}

// TestServer_EphemeralPortIsResolved guards the property the whole test suite
// leans on: asking for port 0 must yield a real, dialable port via Addr.
func TestServer_EphemeralPortIsResolved(t *testing.T) {
	t.Parallel()

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	srv, err := observability.NewServer(0, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want no error", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	if addr := srv.Addr(); addr == nil {
		t.Fatal("Addr() = nil, want a resolved address")
	}
	if port := srv.Addr().(*net.TCPAddr).Port; port == 0 {
		t.Error("Addr() port = 0, want the OS-assigned port")
	}
}
