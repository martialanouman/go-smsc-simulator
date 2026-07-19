package smsc_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

const (
	testSystemID = "smppclient1"
	testPassword = "secret"
)

// healthyConfig builds a single healthy virtual SMSC on an ephemeral port with zero
// served latency, so the end-to-end tests stay fast.
func healthyConfig(name string) config.VirtualSMSCConfig {
	zero := uint64(0)
	return config.VirtualSMSCConfig{
		Name:            name,
		Port:            0,
		BindCredentials: config.BindCredentials{SystemID: testSystemID, Password: testPassword},
		AddrTON:         1,
		AddrNPI:         1,
		PDUBufferSize:   100,
		Scenario: config.ScenarioConfig{
			Profile: config.ProfileHealthy,
			Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: &zero}},
		},
	}
}

func pu64(v uint64) *uint64   { return &v }
func pf64(v float64) *float64 { return &v }
func pint(v int) *int         { return &v }

// baseConfig is the shared skeleton for profile-specific fixtures: one virtual SMSC on
// an ephemeral port with the given scenario and optional seed.
func baseConfig(name string, seed *uint64, sc config.ScenarioConfig) config.VirtualSMSCConfig {
	return config.VirtualSMSCConfig{
		Name:            name,
		Port:            0,
		BindCredentials: config.BindCredentials{SystemID: testSystemID, Password: testPassword},
		AddrTON:         1,
		AddrNPI:         1,
		PDUBufferSize:   100,
		Seed:            seed,
		Scenario:        sc,
	}
}

func deadCarrierConfig(name string, mode config.DeadCarrierMode) config.VirtualSMSCConfig {
	return baseConfig(name, pu64(1), config.ScenarioConfig{
		Profile: config.ProfileDeadCarrier,
		Params:  config.ScenarioParams{Mode: &mode},
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
}

func throttlingConfig(name string, capPerSec int) config.VirtualSMSCConfig {
	return baseConfig(name, pu64(1), config.ScenarioConfig{
		Profile: config.ProfileThrottlingCarrier,
		Params:  config.ScenarioParams{ThroughputCapPerSec: pint(capPerSec), ErrorCode: func() *config.SMPPErrorCode { c := config.ErrorCodeRThrottled; return &c }()},
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
}

func slowConfig(name string) config.VirtualSMSCConfig {
	return baseConfig(name, pu64(1), config.ScenarioConfig{
		Profile: config.ProfileSlowCarrier,
		Latency: config.LatencyConfig{Distribution: config.LatencyUniform, Params: config.LatencyParams{MinMS: pu64(2000), MaxMS: pu64(4000)}},
	})
}

// flakyConfig builds a seeded flaky-carrier with zero latency (fast replay) and no
// periodic disconnect, so the synchronous client can drive a fixed submit sequence.
func flakyConfig(name string, seed uint64) config.VirtualSMSCConfig {
	return baseConfig(name, pu64(seed), config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate: pf64(0.8),
			ErrorMix:    map[config.SMPPErrorCode]uint{config.ErrorCodeRSysErr: 1, config.ErrorCodeRSubmitFail: 1},
		},
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
}

// harness starts an engine and its read-only surface, returning the SMPP dial
// address of the named virtual SMSC and the surface's base URL.
type harness struct {
	engine   *smsc.Engine
	smppAddr string
	baseURL  string
}

// start boots a single healthy virtual SMSC — the default for tests that only need a
// working endpoint.
func start(t *testing.T, name string) harness {
	t.Helper()
	return startWith(t, healthyConfig(name))
}

// startWith boots a single virtual SMSC from an arbitrary config, so profile-specific
// tests can drive flaky / dead / throttling / slow behaviour end-to-end.
func startWith(t *testing.T, cfg config.VirtualSMSCConfig) harness {
	t.Helper()

	name := cfg.Name
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	engine, err := smsc.New([]config.VirtualSMSCConfig{cfg}, nil, logger)
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

	srv, err := observability.NewServer(0, logger, engine, nil)
	if err != nil {
		t.Fatalf("observability.NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	addr, ok := engine.Addr(name)
	if !ok {
		t.Fatalf("engine.Addr(%q) not found", name)
	}
	port := addr.(*net.TCPAddr).Port

	return harness{
		engine:   engine,
		smppAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		baseURL:  "http://" + srv.Addr().String(),
	}
}

// TestE2E_BindSubmitInspect is the walking skeleton (plan §6 acceptance): a client
// binds, submits, gets ESME_ROK, and the submit_sm becomes visible on the read-only
// surface with correct addresses, content and clocks.
func TestE2E_BindSubmitInspect(t *testing.T) {
	t.Parallel()

	h := start(t, "carrier-healthy")
	client := smpptest.Dial(t, h.smppAddr)

	bindResp := client.BindTransceiver(testSystemID, testPassword)
	if bindResp.CommandID != smpp.BindTransceiverResp || bindResp.CommandStatus != smpp.StatusROK {
		t.Fatalf("bind resp = %s/%d, want bind_transceiver_resp/ROK", bindResp.CommandID, bindResp.CommandStatus)
	}
	if body, ok := bindResp.Body.(*smpp.BindResp); !ok || body.SystemID != "carrier-healthy" {
		t.Fatalf("bind resp body = %+v, want system_id carrier-healthy", bindResp.Body)
	}

	submitResp := client.Submit("33600000000", "33611111111", "hello world")
	if submitResp.CommandID != smpp.SubmitSMResp || submitResp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit resp = %s/%d, want submit_sm_resp/ROK", submitResp.CommandID, submitResp.CommandStatus)
	}
	body, ok := submitResp.Body.(*smpp.SubmitResp)
	if !ok || body.MessageID != "1-0001" {
		t.Fatalf("submit resp message_id = %+v, want deterministic 1-0001", submitResp.Body)
	}

	// enquire_link keeps the link alive.
	if el := client.EnquireLink(); el.CommandID != smpp.EnquireLinkResp {
		t.Fatalf("enquire_link resp = %s, want enquire_link_resp", el.CommandID)
	}

	// The submitted PDU is inspectable, content and addresses intact.
	var pdus []observability.RecordedPDUView
	decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-healthy/received-pdus", &pdus)
	if len(pdus) != 1 {
		t.Fatalf("received-pdus = %d, want 1", len(pdus))
	}
	got := pdus[0]
	if got.SourceAddr != "33600000000" || got.DestAddr != "33611111111" {
		t.Errorf("recorded addresses = %s→%s, want 33600000000→33611111111", got.SourceAddr, got.DestAddr)
	}
	if string(got.ShortMessage) != "hello world" {
		t.Errorf("recorded content = %q, want %q", got.ShortMessage, "hello world")
	}
	if got.MessageID != "1-0001" {
		t.Errorf("recorded message_id = %q, want 1-0001", got.MessageID)
	}

	// logical_clock reflects the one submit_sm.
	var clock map[string]uint64
	decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-healthy/logical-clock", &clock)
	if clock["logical_clock"] != 1 {
		t.Errorf("logical_clock = %d, want 1", clock["logical_clock"])
	}
}

// TestE2E_UnbindReleasesBind checks the bind is visible while bound and gone after a
// graceful unbind (plan §6 acceptance).
func TestE2E_UnbindReleasesBind(t *testing.T) {
	t.Parallel()

	h := start(t, "carrier-healthy")
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	var binds []observability.BindView
	decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-healthy/binds", &binds)
	if len(binds) != 1 || binds[0].SystemID != testSystemID || binds[0].BindType != "transceiver" {
		t.Fatalf("binds while bound = %+v, want one transceiver for %s", binds, testSystemID)
	}

	if resp := client.Unbind(); resp.CommandID != smpp.UnbindResp || resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("unbind resp = %s/%d, want unbind_resp/ROK", resp.CommandID, resp.CommandStatus)
	}

	// Deregistration happens in the server's teardown, just after the response is
	// written, so poll until the bind clears rather than assuming it is instant.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var after []observability.BindView
		decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-healthy/binds", &after)
		if len(after) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bind still present %v after unbind", after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestE2E_WrongCredentialsRejected checks that a bad password is answered with
// ESME_RBINDFAIL and never registers a bind.
func TestE2E_WrongCredentialsRejected(t *testing.T) {
	t.Parallel()

	h := start(t, "carrier-healthy")
	client := smpptest.Dial(t, h.smppAddr)

	resp := client.BindTransceiver(testSystemID, "wrong-password")
	if resp.CommandStatus != smpp.StatusBindFail {
		t.Fatalf("bind resp status = %d, want ESME_RBINDFAIL (0x0D)", resp.CommandStatus)
	}

	var binds []observability.BindView
	decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-healthy/binds", &binds)
	if len(binds) != 0 {
		t.Errorf("binds after failed auth = %+v, want none", binds)
	}
}

// TestE2E_GracefulShutdownUnbindsBoundClients checks that engine shutdown sends a
// server-initiated unbind to bound clients rather than dropping the TCP connection
// (CLAUDE.md: "unbind propre des binds sur SIGTERM"), so the gateway under test sees
// a clean unbind, not a reset that could trip its reconnect/circuit-breaker logic.
func TestE2E_GracefulShutdownUnbindsBoundClients(t *testing.T) {
	t.Parallel()

	h := start(t, "carrier-healthy")
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// Shutdown from another goroutine so the client's blocking read stays on the test
	// goroutine (where a failed read may call t.Fatalf).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.engine.Shutdown(ctx)
	}()

	if pdu := client.Read(); pdu.CommandID != smpp.Unbind {
		t.Fatalf("on graceful shutdown got %s, want a server-initiated unbind", pdu.CommandID)
	}
}

// TestE2E_DeadCarrierRejectsBind: dead-carrier in reject_bind mode answers every bind
// with ESME_RBINDFAIL and registers nothing, regardless of credentials.
func TestE2E_DeadCarrierRejectsBind(t *testing.T) {
	t.Parallel()

	h := startWith(t, deadCarrierConfig("carrier-dead-reject", config.DeadCarrierRejectBind))
	client := smpptest.Dial(t, h.smppAddr)

	if pdu := client.BindTransceiver(testSystemID, testPassword); pdu.CommandStatus != smpp.StatusBindFail {
		t.Fatalf("dead-carrier bind status = %d, want ESME_RBINDFAIL", pdu.CommandStatus)
	}
	var binds []observability.BindView
	decodeGET(t, h.baseURL+"/v1/virtual-smscs/carrier-dead-reject/binds", &binds)
	if len(binds) != 0 {
		t.Fatalf("rejected bind must not be registered, got %d", len(binds))
	}
}

// TestE2E_DeadCarrierTimeoutAll: dead-carrier in timeout_all mode accepts the bind but
// withholds every submit_sm_resp.
func TestE2E_DeadCarrierTimeoutAll(t *testing.T) {
	t.Parallel()

	h := startWith(t, deadCarrierConfig("carrier-dead-timeout", config.DeadCarrierTimeoutAll))
	client := smpptest.Dial(t, h.smppAddr)
	if pdu := client.BindTransceiver(testSystemID, testPassword); pdu.CommandStatus != smpp.StatusROK {
		t.Fatalf("timeout_all bind status = %d, want ESME_ROK", pdu.CommandStatus)
	}

	client.SubmitAsync("33600000000", "33611111111", "hello")
	client.ExpectNoResponse(500 * time.Millisecond)
}

// TestE2E_ThrottlingBeyondCap: with a cap of one per second, the first submit succeeds
// and the burst that follows (well within the same second) is throttled.
func TestE2E_ThrottlingBeyondCap(t *testing.T) {
	t.Parallel()

	h := startWith(t, throttlingConfig("carrier-throttle", 1))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	if got := client.SubmitStatus("33600000000", "33611111111", "one"); got != smpp.StatusROK {
		t.Fatalf("first submit under cap = %d, want ESME_ROK", got)
	}
	for k := 0; k < 5; k++ {
		if got := client.SubmitStatus("33600000000", "33611111111", "more"); got != smpp.StatusThrottled {
			t.Fatalf("over-cap submit = %d, want ESME_RTHROTTLED", got)
		}
	}
}

// TestE2E_SlowCarrierBoundedLatency: a slow-carrier submit succeeds after a real delay
// inside the 2–4 s window. This exercises the actual served latency, so it is skipped
// in -short.
func TestE2E_SlowCarrierBoundedLatency(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("slow-carrier serves multi-second latency; skipped in -short")
	}

	h := startWith(t, slowConfig("carrier-slow"))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// The response can take up to 4 s, past the client's default read timeout, so read
	// it with a wider deadline.
	begin := time.Now()
	client.SubmitAsync("33600000000", "33611111111", "slow")
	pdu := client.ReadWithin(6 * time.Second)
	elapsed := time.Since(begin)

	if pdu.CommandStatus != smpp.StatusROK {
		t.Fatalf("slow-carrier status = %d, want ESME_ROK", pdu.CommandStatus)
	}
	// Lower bound loosened slightly for scheduling slack; upper bound guards the cap.
	if elapsed < 1900*time.Millisecond || elapsed > 4500*time.Millisecond {
		t.Fatalf("slow-carrier latency %v outside ~[2s,4s]", elapsed)
	}
}

// TestE2E_FlakyDeterministicReplay corroborates invariant (a) on the wire: two
// independent engines with the same seed and same virtual-SMSC name produce an
// identical per-bind sequence of statuses and message ids for the same submits.
func TestE2E_FlakyDeterministicReplay(t *testing.T) {
	t.Parallel()

	const seed = uint64(20260718)
	const n = 40

	run := func() [][2]string {
		// Both runs use the SAME name: the seed derivation keys on it, so a different
		// name would (correctly) yield a different stream.
		h := startWith(t, flakyConfig("carrier-flaky", seed))
		client := smpptest.Dial(t, h.smppAddr)
		client.BindTransceiver(testSystemID, testPassword)

		out := make([][2]string, n)
		for k := 0; k < n; k++ {
			pdu := client.Submit("33600000000", "33611111111", "msg")
			id := ""
			if r, ok := pdu.Body.(*smpp.SubmitResp); ok {
				id = r.MessageID
			}
			out[k] = [2]string{strconv.FormatUint(uint64(pdu.CommandStatus), 10), id}
		}
		return out
	}

	first, second := run(), run()
	for k := range first {
		if first[k] != second[k] {
			t.Fatalf("replay diverged at submit %d: %v vs %v", k, first[k], second[k])
		}
	}
}

// TestE2E_ShutdownNotDelayedByInFlightSubmit guards the loop-around shutdown path: a
// submit in flight (server mid-latency) is abandoned when quit closes, and readLoop
// loops back to the top with quit already closed — it must exit promptly, not re-arm
// a far-future idle deadline and stall until idleTimeout. It catches removal of the
// readLoop quit-check. (The blocked-in-ReadPDU path is covered by the graceful-
// shutdown test; the exact re-arm-vs-closer clobber interleaving is a ~2-instruction
// window that is not deterministically reproducible — its safety rests on the
// happens-before argument documented at the quit-check.)
func TestE2E_ShutdownNotDelayedByInFlightSubmit(t *testing.T) {
	t.Parallel()

	h := startWith(t, slowConfig("carrier-slow-shutdown"))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// Put a submit in flight: the server is now sleeping on the 2–4 s slow-carrier
	// latency when we shut down.
	client.SubmitAsync("33600000000", "33611111111", "slow")
	time.Sleep(200 * time.Millisecond) // let the server reach serveLatency

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	begin := time.Now()
	if err := h.engine.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown during in-flight submit: %v", err)
	}
	if d := time.Since(begin); d > 2*time.Second {
		t.Fatalf("Shutdown took %v — the idle read deadline stalled teardown", d)
	}
}

func decodeGET(t *testing.T, url string, dst any) {
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
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
