package scenario

import (
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// Local helpers: this file is package scenario (white-box), so it cannot see the
// helpers defined in the external scenario_test package.
func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

func fixedLatency(ms uint64) config.LatencyConfig {
	return config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: &ms}}
}

func uniformLatency(min, max uint64) config.LatencyConfig {
	return config.LatencyConfig{Distribution: config.LatencyUniform, Params: config.LatencyParams{MinMS: &min, MaxMS: &max}}
}

// TestNoGateOnDeterministicProfiles is the structural anti-wall-clock guard: the only
// path that reads the wall clock is the throughput gate, so a seeded bind on a
// deterministic profile must carry no gate at all. If one appeared, invariant (a)
// would silently break for that profile.
func TestNoGateOnDeterministicProfiles(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	timeoutAll := config.DeadCarrierTimeoutAll
	deterministic := []config.ScenarioConfig{
		{Profile: config.ProfileHealthy, Latency: fixedLatency(10)},
		{Profile: config.ProfileFlakyCarrier, Params: config.ScenarioParams{SuccessRate: f64(0.8)}, Latency: fixedLatency(10)},
		{Profile: config.ProfileSlowCarrier, Latency: uniformLatency(2000, 4000)},
		{Profile: config.ProfileDeadCarrier, Params: config.ScenarioParams{Mode: &timeoutAll}, Latency: fixedLatency(0)},
	}
	for _, cfg := range deterministic {
		st := New(cfg, nil).NewBindState(&seed, "smsc", 1)
		if st.gate != nil {
			t.Errorf("profile %q must have no wall-clock gate", cfg.Profile)
		}
	}
}

// TestGateOnCappedPaths confirms the gate IS built where a throughput cap applies:
// the two throughput profiles, and any profile carrying a vSMSC throughput limit.
func TestGateOnCappedPaths(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	capped := []config.ScenarioConfig{
		{Profile: config.ProfileThrottlingCarrier, Params: config.ScenarioParams{ThroughputCapPerSec: i(5000)}, Latency: fixedLatency(40)},
		{Profile: config.ProfileThroughputCapped, Params: config.ScenarioParams{ThroughputCapPerSec: i(8000)}, Latency: fixedLatency(30)},
	}
	for _, cfg := range capped {
		if st := New(cfg, nil).NewBindState(&seed, "smsc", 1); st.gate == nil {
			t.Errorf("profile %q must have a throughput gate", cfg.Profile)
		}
	}

	// A vSMSC-level limit gates even an otherwise-deterministic profile.
	if st := New(config.ScenarioConfig{Profile: config.ProfileHealthy, Latency: fixedLatency(10)}, i(1000)).NewBindState(&seed, "smsc", 1); st.gate == nil {
		t.Errorf("throughput_limit_per_sec must attach a gate independently of the profile")
	}
}
