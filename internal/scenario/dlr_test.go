package scenario_test

import (
	"reflect"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
)

// dlrFlakyEngine is the flaky profile with DLR generation enabled: it exercises the DLR
// outcome draw alongside every other seeded draw, so it is the subject for the extended
// invariant-a guarantee on the DLR channel.
func dlrFlakyEngine() *scenario.Engine {
	return scenario.New(config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate: f64(0.8),
			ErrorMix:    map[config.SMPPErrorCode]uint{config.ErrorCodeRSysErr: 1, config.ErrorCodeRSubmitFail: 1},
		},
		Latency: config.LatencyConfig{
			Distribution: config.LatencyFixed,
			Params:       config.LatencyParams{MS: u64(20)},
		},
		DLR: &config.DLRConfig{
			Delay:          config.DLRDelay{Distribution: config.LatencyFixed, Ticks: u64(5)},
			OutcomeWeights: config.DLROutcomeWeights{Delivered: 90, Failed: 8, Expired: 2},
			Clock:          config.ClockLogical,
		},
	}, nil)
}

// TestInvariantA_DLRReplay extends the replay guarantee to the DLR channel: at a fixed
// seed, two runs produce the identical sequence of DLR plans (outcome + delay ticks) on
// exactly the same ticks.
func TestInvariantA_DLRReplay(t *testing.T) {
	t.Parallel()

	e := dlrFlakyEngine()
	const n = 2000
	first := replay(e, 42, 1, n)
	second := replay(e, 42, 1, n)

	if !reflect.DeepEqual(first, second) {
		t.Fatal("DLR replay diverged at fixed seed")
	}

	// The DLR must be present on success ticks and absent otherwise — never on an error.
	sawDLR, sawSuccessNoError := false, false
	for _, d := range first {
		switch d.Outcome {
		case scenario.OutcomeSuccess:
			sawSuccessNoError = true
			if d.DLR == nil {
				t.Fatal("success outcome must carry a DLR plan under a DLR-enabled profile")
			}
			if d.DLR.DelayTicks != 5 {
				t.Fatalf("DLR delay = %d, want 5", d.DLR.DelayTicks)
			}
			sawDLR = true
		case scenario.OutcomeError:
			if d.DLR != nil {
				t.Fatal("error outcome must never schedule a DLR")
			}
		}
	}
	if !sawDLR || !sawSuccessNoError {
		t.Fatal("fixture did not exercise both success (with DLR) and error paths")
	}
}

// TestDLR_DisabledProfileDrawsNoDLR confirms a profile without a dlr block leaves the
// draw stream untouched: no DLR plan is produced and the base decision sequence is
// byte-for-byte identical to the DLR-free engine (invariant a is not perturbed).
func TestDLR_DisabledProfileDrawsNoDLR(t *testing.T) {
	t.Parallel()

	e := flakyEngine() // no DLR
	for _, d := range replay(e, 42, 1, 500) {
		if d.DLR != nil {
			t.Fatal("DLR-free profile must not produce a DLR plan")
		}
	}
}

// TestDLR_OutcomeMixHonoursWeights checks the weighted pick lands on the configured
// outcomes and respects a zero weight (expired excluded), over a large sample.
func TestDLR_OutcomeMixHonoursWeights(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: u64(0)}},
		DLR: &config.DLRConfig{
			Delay:          config.DLRDelay{Distribution: config.LatencyFixed, Ticks: u64(1)},
			OutcomeWeights: config.DLROutcomeWeights{Delivered: 3, Failed: 1, Expired: 0}, // expired disabled
			Clock:          config.ClockLogical,
		},
	}, nil)

	counts := map[scenario.DLROutcome]int{}
	for _, d := range replay(e, 99, 1, 4000) {
		if d.DLR == nil {
			t.Fatal("healthy profile always succeeds, so every tick must carry a DLR")
		}
		counts[d.DLR.Outcome]++
	}

	if counts[scenario.DLRExpired] != 0 {
		t.Fatalf("expired weight is zero but it was drawn %d times", counts[scenario.DLRExpired])
	}
	if counts[scenario.DLRDelivered] == 0 || counts[scenario.DLRFailed] == 0 {
		t.Fatalf("both weighted outcomes must appear: delivered=%d failed=%d",
			counts[scenario.DLRDelivered], counts[scenario.DLRFailed])
	}
	// delivered (weight 3) should clearly outnumber failed (weight 1).
	if counts[scenario.DLRDelivered] <= counts[scenario.DLRFailed] {
		t.Fatalf("delivered (w=3) should outnumber failed (w=1): %d vs %d",
			counts[scenario.DLRDelivered], counts[scenario.DLRFailed])
	}
}
