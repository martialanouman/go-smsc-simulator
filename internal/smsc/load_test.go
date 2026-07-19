//go:build loadtest

// This file holds the load / NFR harness (plan §11 / T4). It is behind the `loadtest`
// build tag so it never runs in the normal `make test` / CI unit job — it is heavy and
// timing-sensitive. Run it with `make loadtest`.
//
// It validates the throughput and memory NFRs in-process over a loopback socket, which
// is cheap and deterministic. The parts that only mean anything against the real
// container image — cold start < 2s and RSS < 50 MiB — are measured from loadtest/README.md
// instead, since an in-process HeapAlloc is only a proxy for container RSS.

package smsc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

// throughputNFR is the per-virtual-SMSC sustained rate the simulator must hold (plan §11).
const throughputNFR = 15000.0

// startLoad boots a single virtual SMSC and returns its SMPP dial address. It mirrors
// startWith but takes testing.TB, so a Benchmark can share it with the Tests.
func startLoad(tb testing.TB, cfg config.VirtualSMSCConfig) string {
	tb.Helper()

	logger := observability.NewLogger(io.Discard, slog.LevelWarn)
	engine, err := smsc.New([]config.VirtualSMSCConfig{cfg}, nil, logger)
	if err != nil {
		tb.Fatalf("smsc.New: %v", err)
	}
	go func() { _ = engine.Serve() }()
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	addr, ok := engine.Addr(cfg.Name)
	if !ok {
		tb.Fatalf("engine.Addr(%q) not found", cfg.Name)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.(*net.TCPAddr).Port))
}

// drive binds one client and sends count submit_sm synchronously, returning how many
// resolved to ESME_ROK. Synchronous round-trips per connection are latency-bound, so
// throughput comes from running several of these concurrently — as several binds on one
// virtual SMSC would in production.
func drive(tb testing.TB, addr string, count int) (rok int) {
	tb.Helper()

	c := smpptest.Dial(tb, addr)
	if resp := c.BindTransceiver(testSystemID, testPassword); resp.CommandStatus != smpp.StatusROK {
		tb.Fatalf("bind status = %d, want ESME_ROK", resp.CommandStatus)
	}
	for i := 0; i < count; i++ {
		if c.SubmitStatus("111", "222", "load") == smpp.StatusROK {
			rok++
		}
	}
	return rok
}

// TestLoad_ThroughputNFR asserts one virtual SMSC sustains >= 15000 msg/s across a
// handful of concurrent binds (healthy, zero latency).
func TestLoad_ThroughputNFR(t *testing.T) {
	addr := startLoad(t, healthyConfig("load-throughput"))

	const (
		conns   = 8
		perConn = 20000
		total   = conns * perConn
	)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			drive(t, addr, perConn)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	rate := float64(total) / elapsed.Seconds()
	t.Logf("throughput: %.0f msg/s (%d submits in %s across %d binds)", rate, total, elapsed.Round(time.Millisecond), conns)
	if rate < throughputNFR {
		t.Fatalf("throughput %.0f msg/s below NFR %.0f msg/s", rate, throughputNFR)
	}
}

// TestLoad_DeterminismUnderLoad drives many concurrent binds on a seeded flaky-carrier
// and asserts each bind's success rate lands near the configured 0.8 — the per-bind
// statistical guarantee (invariant a is scoped per bind, not across binds). It proves
// concurrency does not corrupt one bind's PRNG stream, without asserting inter-bind order.
func TestLoad_DeterminismUnderLoad(t *testing.T) {
	const (
		conns     = 8
		perConn   = 5000
		wantRate  = 0.8
		tolerance = 0.05 // ~8 sigma at n=5000; a real corruption moves the mean far more
	)
	addr := startLoad(t, flakyConfig("load-determinism", 42))

	rates := make([]float64, conns)
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rates[idx] = float64(drive(t, addr, perConn)) / float64(perConn)
		}(i)
	}
	wg.Wait()

	for i, r := range rates {
		if r < wantRate-tolerance || r > wantRate+tolerance {
			t.Errorf("bind %d success rate %.3f outside [%.2f, %.2f]", i, r, wantRate-tolerance, wantRate+tolerance)
		}
	}
}

// TestLoad_IdleMemory is an in-process proxy for the < 50 MiB/vSMSC-at-rest NFR: after
// binding many idle connections, the heap must stay well under the bound. The real number
// is the container RSS (loadtest/README.md); this only catches a gross per-bind leak.
func TestLoad_IdleMemory(t *testing.T) {
	const idleBinds = 200
	addr := startLoad(t, healthyConfig("load-memory"))

	clients := make([]*smpptest.Client, idleBinds)
	for i := range clients {
		c := smpptest.Dial(t, addr)
		if resp := c.BindTransceiver(testSystemID, testPassword); resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("bind %d status = %d, want ESME_ROK", i, resp.CommandStatus)
		}
		clients[i] = c
	}
	// Keep the clients reachable past the measurement so their connections stay bound.
	runtime.KeepAlive(clients)

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	const bound = 50 << 20 // 50 MiB
	t.Logf("heap at rest with %d idle binds: %.1f MiB", idleBinds, float64(ms.HeapAlloc)/(1<<20))
	if ms.HeapAlloc > bound {
		t.Fatalf("heap %d bytes exceeds the %d-byte at-rest bound", ms.HeapAlloc, bound)
	}
}

// BenchmarkThroughput reports submit_sm throughput for profiling and regression tracking.
// It saturates one virtual SMSC with concurrent binds and reports msg/s via a custom metric.
func BenchmarkThroughput(b *testing.B) {
	addr := startLoad(b, healthyConfig("bench-throughput"))

	const conns = 8
	var done atomic.Int64

	b.ResetTimer()
	start := time.Now()
	var wg sync.WaitGroup
	per := b.N / conns
	if per == 0 {
		per = 1
	}
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done.Add(int64(drive(b, addr, per)))
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	b.StopTimer()

	b.ReportMetric(float64(conns*per)/elapsed.Seconds(), "msg/s")
	_ = done.Load()
}
