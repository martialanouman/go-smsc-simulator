package smsc_test

import (
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// randomDisconnectCut binds one session against a fresh seeded engine whose only rule is a
// scope: random disconnect at tick 1 (after_response), drives the one submit that reaches
// that tick, and reports whether the bind was cut. The whole thing is a pure function of the
// seed (and the deterministic first bind ordinal), so two calls with the same seed must
// agree — that is invariant (a) for the one new random decision S5 introduces.
func randomDisconnectCut(t *testing.T, seed uint64) bool {
	t.Helper()

	cfg := baseConfig("carrier-rand", pu64(seed), config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
	cfg.ScheduledDisconnects = []config.ScheduledDisconnect{
		{AtTick: 1, Scope: config.DisconnectScopeRandom, When: config.DisconnectAfterResponse},
	}
	h := startWith(t, cfg)
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// after_response: the submit at tick 1 is answered, then the bind is cut iff this bind is
	// the random target.
	if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit tick 1 = %d, want ROK", resp.CommandStatus)
	}
	return client.ClosedWithin(300 * time.Millisecond)
}

// TestE2E_S5_RandomDisconnectDeterministic is invariant (a) for scope: random — the one
// wall-clock-free random decision S5 adds. Same seed replays the same cut decision; and
// across seeds both outcomes occur, so the decision is genuinely seed-driven, not a constant.
func TestE2E_S5_RandomDisconnectDeterministic(t *testing.T) {
	t.Parallel()

	// Reproducible: two independent runs of the same seed agree.
	firstCut := randomDisconnectCut(t, 7)
	secondCut := randomDisconnectCut(t, 7)
	if firstCut != secondCut {
		t.Fatal("scope: random disconnect not reproducible: same seed gave different cut decisions")
	}

	// Seed-driven: over a spread of seeds, the bind is sometimes cut and sometimes spared.
	var sawCut, sawAlive bool
	for s := uint64(1); s <= 12 && (!sawCut || !sawAlive); s++ {
		if randomDisconnectCut(t, s) {
			sawCut = true
		} else {
			sawAlive = true
		}
	}
	if !sawCut || !sawAlive {
		t.Fatalf("scope: random degenerate across seeds: sawCut=%v sawAlive=%v", sawCut, sawAlive)
	}
}

// randomCutAtTick binds one session against a fresh seeded engine whose only rule is a
// scope: random disconnect at atTick (after_response), drives `submits` submits, then reports
// whether the bind was cut — either by the normal drain (submits >= atTick, voie a) or by the
// quiescence flush (submits < atTick, voie b, thanks to the short flush window).
func randomCutAtTick(t *testing.T, seed, atTick uint64, submits int) bool {
	t.Helper()

	cfg := baseConfig("carrier-rand-tick", pu64(seed), config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
	cfg.ScheduledDisconnects = []config.ScheduledDisconnect{
		{AtTick: atTick, Scope: config.DisconnectScopeRandom, When: config.DisconnectAfterResponse},
	}
	cfg.QuiescenceFlushMs = pu64(60)
	h := startWith(t, cfg)
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	for i := 0; i < submits; i++ {
		if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("submit %d = %d, want ROK (after_response)", i, resp.CommandStatus)
		}
	}
	return client.ClosedWithin(300 * time.Millisecond)
}

// TestE2E_S5_RandomDisconnectTickStable guards that the scope: random decision is a stable
// property of (seed, at_tick), independent of the drain path: reaching the disconnect via the
// normal drain (busy up to at_tick) and via the quiescence flush (one submit then silence)
// must agree. Keying the coin on the live clock instead of at_tick would make them diverge.
func TestE2E_S5_RandomDisconnectTickStable(t *testing.T) {
	t.Parallel()

	const atTick = 3
	for seed := uint64(1); seed <= 6; seed++ {
		viaDrain := randomCutAtTick(t, seed, atTick, atTick) // voie a: reaches at_tick
		viaFlush := randomCutAtTick(t, seed, atTick, 1)      // voie b: flushed short of at_tick
		if viaDrain != viaFlush {
			t.Fatalf("seed %d: random cut differs by drain path (voie a=%v, voie b=%v); the coin must key on at_tick",
				seed, viaDrain, viaFlush)
		}
	}
}

// TestE2E_S5_MOSequenceReproducible replays a scheduled MO sequence: the same seeded fixture,
// driven the same way, delivers the same MOs in the same order — the MO mechanism is anchored
// to per_bind_clock and does not perturb (or depend on) the scenario PRNG.
func TestE2E_S5_MOSequenceReproducible(t *testing.T) {
	t.Parallel()

	moConfig := func() config.VirtualSMSCConfig {
		cfg := baseConfig("carrier-mo-replay", pu64(3), config.ScenarioConfig{
			Profile: config.ProfileHealthy,
			Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
		})
		cfg.MOInjection = &config.MOInjectionConfig{
			Mode:  config.MOModeScheduled,
			Clock: config.ClockLogical,
			Events: []config.MOEvent{
				{AtTick: 2, SourceAddr: "111", DestAddr: "999", Content: "mo-two"},
				{AtTick: 3, SourceAddr: "222", DestAddr: "999", Content: "mo-three"},
				{AtTick: 5, SourceAddr: "333", DestAddr: "999", Content: "mo-five"},
			},
		}
		return cfg
	}

	run := func() []string {
		h := startWith(t, moConfig())
		client := smpptest.Dial(t, h.smppAddr)
		client.BindTransceiver(testSystemID, testPassword)
		// Drive five ticks without reading, then collect exactly the three MOs (CollectDeliverSMs
		// skips the interleaved submit_sm_resps).
		for i := 0; i < 5; i++ {
			client.SubmitAsync("33600000000", "33611111111", "m")
		}
		mos := client.CollectDeliverSMs(3, 2*time.Second)
		got := make([]string, 0, len(mos))
		for _, pdu := range mos {
			m := pdu.Body.(*smpp.Message)
			if m.ESMClass != 0 {
				t.Errorf("collected a receipt (esm 0x%02x), want an MO", m.ESMClass)
			}
			got = append(got, string(m.ShortMessage))
		}
		return got
	}

	first, second := run(), run()
	want := []string{"mo-two", "mo-three", "mo-five"}
	for i, w := range want {
		if i >= len(first) || first[i] != w {
			t.Fatalf("MO sequence = %v, want %v", first, want)
		}
	}
	if len(first) != len(second) {
		t.Fatalf("replay length differs: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("MO sequence not reproducible: run1=%v run2=%v", first, second)
		}
	}
}
