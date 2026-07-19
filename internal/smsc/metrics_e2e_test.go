package smsc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/metrics"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

// metricsHarness starts an engine wired to a real Prometheus registry, plus the
// observability surface exposing it, so a test can drive SMPP traffic and then scrape
// GET /metrics (or gather the registry directly).
type metricsHarness struct {
	baseURL string
	addrs   map[string]string // virtual SMSC name -> SMPP dial address
	reg     *prometheus.Registry
}

func startWithMetrics(t *testing.T, cfgs ...config.VirtualSMSCConfig) metricsHarness {
	t.Helper()

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	reg := prometheus.NewRegistry()
	engine, err := smsc.New(cfgs, metrics.New(reg), logger)
	if err != nil {
		t.Fatalf("smsc.New: %v", err)
	}
	go func() { _ = engine.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Errorf("engine.Shutdown: %v", err)
		}
	})

	srv, err := observability.NewServer(0, logger, engine, reg)
	if err != nil {
		t.Fatalf("observability.NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	addrs := make(map[string]string, len(cfgs))
	for _, c := range cfgs {
		addr, ok := engine.Addr(c.Name)
		if !ok {
			t.Fatalf("engine.Addr(%q) not found", c.Name)
		}
		addrs[c.Name] = net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.(*net.TCPAddr).Port))
	}
	return metricsHarness{baseURL: "http://" + srv.Addr().String(), addrs: addrs, reg: reg}
}

func scrapeText(t *testing.T, url string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

// TestE2E_MetricsPerVirtualSMSC: GET /metrics exposes counters and the active-scenario
// gauge labelled per virtual SMSC (S6/T3 acceptance).
func TestE2E_MetricsPerVirtualSMSC(t *testing.T) {
	t.Parallel()

	h := startWithMetrics(t, healthyConfig("carrier-a"), healthyConfig("carrier-b"))

	for _, name := range []string{"carrier-a", "carrier-b"} {
		client := smpptest.Dial(t, h.addrs[name])
		client.BindTransceiver(testSystemID, testPassword)
		if got := client.SubmitStatus("33600000000", "33611111111", "hi"); got != smpp.StatusROK {
			t.Fatalf("%s submit status = %d, want ESME_ROK", name, got)
		}
	}

	body := scrapeText(t, h.baseURL+"/metrics")
	for _, name := range []string{"carrier-a", "carrier-b"} {
		wantSubmit := `smsc_submit_sm_received_total{virtual_smsc="` + name + `"} 1`
		if !strings.Contains(body, wantSubmit) {
			t.Errorf("/metrics missing %q\n%s", wantSubmit, body)
		}
		wantScenario := `smsc_active_scenario{scenario="healthy",virtual_smsc="` + name + `"} 1`
		if !strings.Contains(body, wantScenario) {
			t.Errorf("/metrics missing %q\n%s", wantScenario, body)
		}
		wantOutcome := `smsc_submit_sm_outcome_total{outcome="success",virtual_smsc="` + name + `"} 1`
		if !strings.Contains(body, wantOutcome) {
			t.Errorf("/metrics missing %q\n%s", wantOutcome, body)
		}
	}
}

// TestE2E_MetricsLabelCardinalityBounded is the S6/T3 guard invariant: no metric may
// carry a label outside the bounded set {virtual_smsc, bind_type, outcome, scenario},
// and no label value may leak a MSISDN, message content or message_id. It fails if a
// high-cardinality label is ever introduced (CLAUDE.md: bounded Prometheus labels).
func TestE2E_MetricsLabelCardinalityBounded(t *testing.T) {
	t.Parallel()

	h := startWithMetrics(t, healthyConfig("carrier-a"))
	client := smpptest.Dial(t, h.addrs["carrier-a"])
	client.BindTransceiver(testSystemID, testPassword)
	client.Submit("33600000000", "33611111111", "hello world") // known MSISDNs + content

	allowed := map[string]bool{"virtual_smsc": true, "bind_type": true, "outcome": true, "scenario": true}
	forbiddenValues := map[string]string{
		"33600000000": "source MSISDN",
		"33611111111": "dest MSISDN",
		"hello world": "message content",
		"1-0001":      "message_id",
	}

	families, err := h.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var sawSMSCMetric bool
	for _, mf := range families {
		if !strings.HasPrefix(mf.GetName(), "smsc_") {
			continue
		}
		sawSMSCMetric = true
		for _, metric := range mf.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if !allowed[lp.GetName()] {
					t.Errorf("metric %s carries unbounded label %q (allowed: virtual_smsc, bind_type, outcome, scenario)",
						mf.GetName(), lp.GetName())
				}
				if what, bad := forbiddenValues[lp.GetValue()]; bad {
					t.Errorf("metric %s label %s leaks %s value %q — must never be a label",
						mf.GetName(), lp.GetName(), what, lp.GetValue())
				}
			}
		}
	}
	if !sawSMSCMetric {
		t.Fatal("no smsc_* metric families gathered; the guard scanned nothing")
	}
}
