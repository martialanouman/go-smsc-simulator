package smsc

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// lockedBuffer is a concurrency-safe io.Writer: the engine logs from a session
// goroutine while the test reads the buffer, so both must be serialised.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// isolationHealthyCfg is a zero-latency healthy virtual SMSC on an ephemeral port.
// It is the white-box twin of e2e_test.go's healthyConfig, needed here because this
// file is package smsc (it reaches the unexported testPanicHook seam).
func isolationHealthyCfg(name string) config.VirtualSMSCConfig {
	zero := uint64(0)
	return config.VirtualSMSCConfig{
		Name:            name,
		Port:            0,
		BindCredentials: config.BindCredentials{SystemID: "smppclient1", Password: "secret"},
		AddrTON:         1,
		AddrNPI:         1,
		PDUBufferSize:   100,
		Scenario: config.ScenarioConfig{
			Profile: config.ProfileHealthy,
			Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: &zero}},
		},
	}
}

// TestIsolation_SessionPanicDoesNotKillSiblings proves the S6/T1 per-session recover
// boundary: a session goroutine that panics on one virtual SMSC ("boom") must not take
// down the process, and a sibling instance ("healthy") keeps binding and serving
// submit_sm normally. It also asserts the panic was recovered (not merely never
// triggered) by observing the loud Error log recoverSession emits.
//
// Not t.Parallel: testPanicHook is a package-global seam and setting it must not race
// another test in this package (there is currently none, but keep the invariant).
func TestIsolation_SessionPanicDoesNotKillSiblings(t *testing.T) {
	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Panic on the first handled PDU of any session on "boom"; leave "healthy" untouched.
	// Panic only once "boom" is bound, so the panic strikes a registered session — this
	// exercises the teardown-on-panic path (the bind must still be deregistered).
	testPanicHook = func(s *session) {
		if s.smsc.cfg.Name == "boom" && s.state == stateBound {
			panic("induced session panic")
		}
	}
	t.Cleanup(func() { testPanicHook = nil })

	engine, err := New([]config.VirtualSMSCConfig{isolationHealthyCfg("boom"), isolationHealthyCfg("healthy")}, nil, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = engine.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	dial := func(name string) string {
		t.Helper()
		addr, ok := engine.Addr(name)
		if !ok {
			t.Fatalf("Addr(%q) not found", name)
		}
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.(*net.TCPAddr).Port))
	}

	// Bind "boom" (registers the session), then trigger the panic with SubmitAsync, which
	// writes without reading so the client never blocks on the response the crashed session
	// will never send.
	boom := smpptest.Dial(t, dial("boom"))
	if resp := boom.BindTransceiver("smppclient1", "secret"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("boom bind status = %d, want ESME_ROK before the induced panic", resp.CommandStatus)
	}
	boom.SubmitAsync("33600000000", "33611111111", "boom")

	// Wait for recoverSession to log the recovered panic. Its presence proves the panic
	// fired AND was contained rather than crashing the test binary.
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), "session panic recovered") {
		if time.Now().After(deadline) {
			t.Fatalf("no recovered-panic log within deadline; got:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := logs.String(); !strings.Contains(got, "virtual_smsc=boom") {
		t.Errorf("recovered-panic log missing virtual_smsc=boom, got:\n%s", got)
	}

	// The deferred teardown must run despite the panic: the crashed bind is deregistered,
	// not left orphaned (which would also leak its writeLoop goroutine and FD).
	boomSMSC := engine.byName["boom"]
	for boomSMSC.binds.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("boom bind still registered %v after panic; teardown did not run", boomSMSC.binds.count())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The sibling instance is unaffected: bind and submit succeed after boom's crash.
	healthy := smpptest.Dial(t, dial("healthy"))
	if resp := healthy.BindTransceiver("smppclient1", "secret"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("healthy bind status = %d after sibling panic, want ESME_ROK", resp.CommandStatus)
	}
	if resp := healthy.Submit("33600000000", "33611111111", "still alive"); resp.CommandID != smpp.SubmitSMResp || resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("healthy submit = %s/%d after sibling panic, want submit_sm_resp/ROK", resp.CommandID, resp.CommandStatus)
	}
}

// TestRecoverGoroutine_CatchesPanicAndRunsLaterDefers is the unit guard for the
// writer/closer panic boundary (S6/T1): recoverGoroutine must swallow a panic AND let a
// defer registered before it still run — exactly what writeLoop and the closer rely on
// to close their done channels, so a panic there cannot wedge teardown or escape to
// crash the sibling virtual SMSCs.
func TestRecoverGoroutine_CatchesPanicAndRunsLaterDefers(t *testing.T) {
	var logs lockedBuffer
	s := &session{logger: slog.New(slog.NewTextHandler(&logs, nil))}

	ran := false
	func() {
		defer func() { ran = true }()         // registered first => runs last, must still run
		defer s.recoverGoroutine("writeLoop") // registered second => runs first, recovers
		panic("boom")                         // reaching the end of func() proves it was recovered
	}()

	if !ran {
		t.Fatal("defer registered before recoverGoroutine did not run; teardown would wedge")
	}
	if got := logs.String(); !strings.Contains(got, "session goroutine panic recovered") || !strings.Contains(got, "writeLoop") {
		t.Errorf("recoverGoroutine did not log the recovered panic; got:\n%s", got)
	}
}
