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

// harness starts an engine and its read-only surface, returning the SMPP dial
// address of the named virtual SMSC and the surface's base URL.
type harness struct {
	engine   *smsc.Engine
	smppAddr string
	baseURL  string
}

func start(t *testing.T, name string) harness {
	t.Helper()

	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	engine, err := smsc.New([]config.VirtualSMSCConfig{healthyConfig(name)}, logger)
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

	srv, err := observability.NewServer(0, logger, engine)
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
