package scenario_test

import (
	"reflect"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
)

// flakyEngine builds the profile that exercises every seeded draw together —
// success/error, weighted error code, latency (normal), and periodic disconnect — so
// it is the canonical subject for the replay guarantee (invariant a).
func flakyEngine() *scenario.Engine {
	return scenario.New(config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate:             f64(0.8),
			ErrorMix:                map[config.SMPPErrorCode]uint{config.ErrorCodeRSysErr: 1, config.ErrorCodeRSubmitFail: 1},
			DisconnectIntervalTicks: u64(50),
		},
		Latency: config.LatencyConfig{
			Distribution: config.LatencyNormal,
			Params:       config.LatencyParams{MeanMS: u64(60), StddevMS: u64(15)},
		},
	}, nil)
}

func replay(e *scenario.Engine, seed uint64, ordinal uint64, n int) []scenario.Decision {
	st := e.NewBindState(&seed, "carrier", ordinal)
	out := make([]scenario.Decision, n)
	for k := range out {
		out[k] = e.Evaluate(st, uint64(k+1))
	}
	return out
}

// TestInvariantA_FlakyReplay is the most important test in the project: at a fixed
// seed, two runs of the same fixture produce the same per-bind sequence of outcomes
// and latencies (in ticks). It is byte-for-byte equality via reflect.DeepEqual.
func TestInvariantA_FlakyReplay(t *testing.T) {
	t.Parallel()

	e := flakyEngine()
	const n = 2000
	first := replay(e, 42, 1, n)
	second := replay(e, 42, 1, n)

	if !reflect.DeepEqual(first, second) {
		for k := range first {
			if first[k] != second[k] {
				t.Fatalf("replay diverged at tick %d: %+v vs %+v", k+1, first[k], second[k])
			}
		}
		t.Fatal("replay diverged but no per-tick mismatch found")
	}
}

// TestInvariantA_ScopedPerBind confirms determinism is per bind, not global: two
// different bind ordinals draw decorrelated streams from the same seed.
func TestInvariantA_ScopedPerBind(t *testing.T) {
	t.Parallel()

	e := flakyEngine()
	const n = 2000
	if reflect.DeepEqual(replay(e, 42, 1, n), replay(e, 42, 2, n)) {
		t.Fatal("two bind ordinals produced identical sequences; streams must be per-bind")
	}
}
