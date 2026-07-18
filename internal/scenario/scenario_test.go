package scenario_test

import (
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
)

func fixedLatency(ms uint64) config.LatencyConfig {
	return config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: &ms}}
}

// TestEngine_HealthySucceeds checks the one implemented profile: always success at
// the configured fixed latency, regardless of the clock.
func TestEngine_HealthySucceeds(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{Profile: config.ProfileHealthy, Latency: fixedLatency(20)})
	for clock := uint64(0); clock < 100; clock++ {
		got := e.Evaluate(clock)
		if got.Outcome != scenario.OutcomeSuccess || got.LatencyMS != 20 {
			t.Fatalf("Evaluate(%d) = %+v, want success at 20ms", clock, got)
		}
	}
}

// TestEngine_StubProfilesFallBackToHealthy documents the S2 boundary: the five
// unimplemented profiles must behave as healthy, not error or panic.
func TestEngine_StubProfilesFallBackToHealthy(t *testing.T) {
	t.Parallel()

	profiles := []config.Profile{
		config.ProfileFlakyCarrier, config.ProfileThrottlingCarrier,
		config.ProfileDeadCarrier, config.ProfileSlowCarrier, config.ProfileThroughputCapped,
	}
	for _, p := range profiles {
		e := scenario.New(config.ScenarioConfig{Profile: p, Latency: fixedLatency(5)})
		if got := e.Evaluate(0); got.Outcome != scenario.OutcomeSuccess {
			t.Errorf("profile %q: Outcome = %v, want success (STUB fallback)", p, got.Outcome)
		}
	}
}

// TestEngine_NonFixedLatencyIsZeroForNow pins the documented S2 limitation so a
// future reader knows the zero is intentional, not a bug.
func TestEngine_NonFixedLatencyIsZeroForNow(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyUniform},
	})
	if got := e.Evaluate(0); got.LatencyMS != 0 {
		t.Fatalf("uniform latency served %d ms, want 0 until S3", got.LatencyMS)
	}
}
