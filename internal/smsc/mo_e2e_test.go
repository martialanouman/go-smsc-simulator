package smsc_test

import (
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// moScheduledConfig builds a seeded healthy virtual SMSC (no DLR, so the deliver_sm stream
// carries only MOs) with a scheduled MO due at atTick.
func moScheduledConfig(name string, seed uint64, atTick uint64, source, dest, content string) config.VirtualSMSCConfig {
	cfg := baseConfig(name, pu64(seed), config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
	cfg.MOInjection = &config.MOInjectionConfig{
		Mode:  config.MOModeScheduled,
		Clock: config.ClockLogical,
		Events: []config.MOEvent{
			{AtTick: atTick, SourceAddr: source, DestAddr: dest, Content: content},
		},
	}
	return cfg
}

// TestE2E_MO_ScheduledAtTick is T1: a scheduled mobile-originated message is delivered as a
// deliver_sm — esm_class 0 (an MO, not a 0x04 receipt), the configured content and
// addresses — when the bind's per_bind_clock reaches its at_tick (voie a: the third submit
// advances the clock to 3, releasing the MO due at 3).
func TestE2E_MO_ScheduledAtTick(t *testing.T) {
	t.Parallel()

	const (
		atTick  = 3
		source  = "33699999999"
		dest    = "33600000000"
		content = "hello-mo"
	)
	h := startWith(t, moScheduledConfig("carrier-mo", 1, atTick, source, dest, content))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// Advance the clock to at_tick; each submit_sm_resp is consumed by Submit, so the MO is
	// the only deliver_sm left on the stream.
	for i := 0; i < atTick; i++ {
		resp := client.Submit("33611111111", "33622222222", "m")
		if resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("submit %d = %d, want ROK", i, resp.CommandStatus)
		}
	}

	pdu := client.ReadDeliverSM(time.Second)
	m, ok := pdu.Body.(*smpp.Message)
	if !ok {
		t.Fatalf("deliver_sm body = %T, want *smpp.Message", pdu.Body)
	}
	if m.ESMClass != 0 {
		t.Errorf("MO esm_class = 0x%02x, want 0x00 (mobile-originated, not a receipt)", m.ESMClass)
	}
	if got := string(m.ShortMessage); got != content {
		t.Errorf("MO short_message = %q, want %q", got, content)
	}
	if m.SourceAddr != source || m.DestAddr != dest {
		t.Errorf("MO addrs = %s→%s, want %s→%s", m.SourceAddr, m.DestAddr, source, dest)
	}
}

// TestE2E_MO_ScheduledFlushedAtQuiescence is invariant (d) for MO: an MO scheduled beyond
// the ticks the bind will reach is still drained by the quiescence flush (voie b) once the
// bind falls silent, never frozen.
func TestE2E_MO_ScheduledFlushedAtQuiescence(t *testing.T) {
	t.Parallel()

	const content = "flushed-mo"
	cfg := moScheduledConfig("carrier-mo-flush", 1, 100, "111", "222", content)
	cfg.QuiescenceFlushMs = pu64(60)
	h := startWith(t, cfg)
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// One submit (clock=1, far below at_tick 100) then silence: the flush must release the MO.
	if resp := client.Submit("33611111111", "33622222222", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit = %d, want ROK", resp.CommandStatus)
	}

	pdu := client.ReadDeliverSM(500 * time.Millisecond)
	m := pdu.Body.(*smpp.Message)
	if m.ESMClass != 0 || string(m.ShortMessage) != content {
		t.Errorf("flushed MO = esm 0x%02x %q, want esm 0x00 %q", m.ESMClass, m.ShortMessage, content)
	}
}
