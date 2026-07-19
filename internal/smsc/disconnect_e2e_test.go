package smsc_test

import (
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// disconnectConfig builds a seeded healthy virtual SMSC with a single scheduled disconnect.
func disconnectConfig(name string, atTick uint64, scope config.DisconnectScope, when config.DisconnectWhen) config.VirtualSMSCConfig {
	cfg := baseConfig(name, pu64(1), config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
	cfg.ScheduledDisconnects = []config.ScheduledDisconnect{{AtTick: atTick, Scope: scope, When: when}}
	return cfg
}

// pollBindsEmpty waits until the named virtual SMSC reports no active binds — deregistration
// happens in the server teardown, just after the link drops, so it is not instant.
func pollBindsEmpty(t *testing.T, baseURL, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var binds []observability.BindView
		decodeGET(t, baseURL+"/v1/virtual-smscs/"+name+"/binds", &binds)
		if len(binds) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bind still present after scheduled disconnect: %+v", binds)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestE2E_ScheduledDisconnect_AfterResponse is T2: scope all, after_response — the submit
// that carries the clock to at_tick is answered, then the link is cut and the bind clears
// from /binds.
func TestE2E_ScheduledDisconnect_AfterResponse(t *testing.T) {
	t.Parallel()

	const atTick = 2
	h := startWith(t, disconnectConfig("carrier-disc-after", atTick, config.DisconnectScopeAll, config.DisconnectAfterResponse))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	for i := 0; i < atTick; i++ {
		// after_response: every submit up to and including at_tick is answered.
		if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("submit %d = %d, want ROK", i, resp.CommandStatus)
		}
	}

	client.ExpectClosed()
	pollBindsEmpty(t, h.baseURL, "carrier-disc-after")
}

// TestE2E_ScheduledDisconnect_BeforeResponse is T2: scope all, before_response — the submit
// at at_tick gets NO response and the link is cut (the same seam as an OutcomeDisconnect
// before_response).
func TestE2E_ScheduledDisconnect_BeforeResponse(t *testing.T) {
	t.Parallel()

	const atTick = 2
	h := startWith(t, disconnectConfig("carrier-disc-before", atTick, config.DisconnectScopeAll, config.DisconnectBeforeResponse))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// Submits before the disconnect tick are answered normally.
	for i := 0; i < atTick-1; i++ {
		if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("submit %d = %d, want ROK", i, resp.CommandStatus)
		}
	}
	// The submit at the disconnect tick is withheld and the link cut — send without waiting
	// on a response that never comes.
	client.SubmitAsync("33600000000", "33611111111", "m")
	client.ExpectClosed()
	pollBindsEmpty(t, h.baseURL, "carrier-disc-before")
}

// TestE2E_ScheduledDisconnect_FlushedAtQuiescence is invariant (d) for disconnects: a cut
// scheduled beyond the ticks a bind reaches still fires via the quiescence flush once the
// bind falls silent, rather than being frozen.
func TestE2E_ScheduledDisconnect_FlushedAtQuiescence(t *testing.T) {
	t.Parallel()

	cfg := disconnectConfig("carrier-disc-flush", 100, config.DisconnectScopeAll, config.DisconnectAfterResponse)
	cfg.QuiescenceFlushMs = pu64(60)
	h := startWith(t, cfg)
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit = %d, want ROK", resp.CommandStatus)
	}
	client.ExpectClosed()
	pollBindsEmpty(t, h.baseURL, "carrier-disc-flush")
}
