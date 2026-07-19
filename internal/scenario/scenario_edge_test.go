package scenario_test

import (
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// healthyEdgeConfig is a healthy profile (always success) with protocol edge cases
// enabled — the block is passed through, so cadence/kinds can be tuned per test.
func healthyEdgeConfig(block *config.ProtocolEdgeCasesConfig) config.ScenarioConfig {
	return config.ScenarioConfig{
		Profile:                  config.ProfileHealthy,
		Latency:                  fixedLatency(0),
		ProtocolEdgeCasesEnabled: true,
		ProtocolEdgeCases:        block,
	}
}

// TestEngine_EdgeCase_DisabledNoInjection: with the master switch off, no submit ever
// carries an edge-case plan — strict encoding, whatever the tick.
func TestEngine_EdgeCase_DisabledNoInjection(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	e := scenario.New(config.ScenarioConfig{Profile: config.ProfileHealthy, Latency: fixedLatency(0)}, nil)
	st := e.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 20; tick++ {
		if got := e.Evaluate(st, tick); got.EdgeCase != nil {
			t.Fatalf("Evaluate(%d).EdgeCase = %+v, want nil (injection disabled)", tick, got.EdgeCase)
		}
	}
}

// TestEngine_EdgeCase_DefaultAllKinds: enabled with no tuning block injects on every
// tick, rotating through all three kinds in their fixed order.
func TestEngine_EdgeCase_DefaultAllKinds(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	e := scenario.New(healthyEdgeConfig(nil), nil)
	st := e.NewBindState(&seed, "smsc", 1)

	want := []smpp.EdgeCaseKind{smpp.EdgeBadLength, smpp.EdgeUnknownCmdID, smpp.EdgeBadSequence}
	for tick := uint64(1); tick <= 6; tick++ {
		d := e.Evaluate(st, tick)
		if d.EdgeCase == nil {
			t.Fatalf("Evaluate(%d).EdgeCase = nil, want a plan every tick", tick)
		}
		if got := d.EdgeCase.Kind; got != want[(tick-1)%3] {
			t.Fatalf("Evaluate(%d).EdgeCase.Kind = %s, want %s", tick, got, want[(tick-1)%3])
		}
	}
}

// TestEngine_EdgeCase_CadenceAndSelectedKinds: inject_every_ticks skips the ticks in
// between, and an explicit kinds list is rotated in order across the injecting ticks.
func TestEngine_EdgeCase_CadenceAndSelectedKinds(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	e := scenario.New(healthyEdgeConfig(&config.ProtocolEdgeCasesConfig{
		InjectEveryTicks: u64(2),
		Kinds:            []config.EdgeCaseKind{config.EdgeCaseUnknownCmdID, config.EdgeCaseBadSequence},
	}), nil)
	st := e.NewBindState(&seed, "smsc", 1)

	// everyTicks=2 -> inject on ticks 2,4,6,8; rotate [unknown, bad_sequence].
	want := map[uint64]*smpp.EdgeCaseKind{
		1: nil,
		2: kindPtr(smpp.EdgeUnknownCmdID),
		3: nil,
		4: kindPtr(smpp.EdgeBadSequence),
		5: nil,
		6: kindPtr(smpp.EdgeUnknownCmdID),
		7: nil,
		8: kindPtr(smpp.EdgeBadSequence),
	}
	for tick := uint64(1); tick <= 8; tick++ {
		d := e.Evaluate(st, tick)
		switch exp := want[tick]; {
		case exp == nil && d.EdgeCase != nil:
			t.Fatalf("Evaluate(%d).EdgeCase = %+v, want nil (off-cadence)", tick, d.EdgeCase)
		case exp != nil && d.EdgeCase == nil:
			t.Fatalf("Evaluate(%d).EdgeCase = nil, want %s", tick, *exp)
		case exp != nil && d.EdgeCase.Kind != *exp:
			t.Fatalf("Evaluate(%d).EdgeCase.Kind = %s, want %s", tick, d.EdgeCase.Kind, *exp)
		}
	}
}

// TestEngine_EdgeCase_DoesNotPerturbSeededStream is the invariant-(a) guarantee: enabling
// edge cases on a seeded flaky-carrier leaves the outcome/status/latency stream byte-for-
// byte identical, because injection is a pure tick function that draws no PRNG.
func TestEngine_EdgeCase_DoesNotPerturbSeededStream(t *testing.T) {
	t.Parallel()

	seed := uint64(42)
	params := config.ScenarioParams{
		SuccessRate: f64(0.8),
		ErrorMix:    map[config.SMPPErrorCode]uint{config.ErrorCodeRSysErr: 1, config.ErrorCodeRSubmitFail: 1},
	}
	plain := scenario.New(config.ScenarioConfig{Profile: config.ProfileFlakyCarrier, Params: params, Latency: uniformLatency(10, 50)}, nil)
	edged := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier, Params: params, Latency: uniformLatency(10, 50),
		ProtocolEdgeCasesEnabled: true,
	}, nil)

	sp := plain.NewBindState(&seed, "smsc", 1)
	se := edged.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 200; tick++ {
		dp := plain.Evaluate(sp, tick)
		de := edged.Evaluate(se, tick)
		if dp.Outcome != de.Outcome || dp.Status != de.Status || dp.LatencyMS != de.LatencyMS {
			t.Fatalf("tick %d: edge injection perturbed the seeded stream\n plain: %+v\n edged: %+v", tick, dp, de)
		}
	}
}

func kindPtr(k smpp.EdgeCaseKind) *smpp.EdgeCaseKind { return &k }
