package scenario_test

import (
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }
func i(v int) *int           { return &v }

func fixedLatency(ms uint64) config.LatencyConfig {
	return config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: &ms}}
}

func uniformLatency(min, max uint64) config.LatencyConfig {
	return config.LatencyConfig{Distribution: config.LatencyUniform, Params: config.LatencyParams{MinMS: &min, MaxMS: &max}}
}

func TestEngine_ProfileReportsCatalogueProfile(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{Profile: config.ProfileSlowCarrier, Latency: uniformLatency(2000, 4000)}, nil)
	if got := e.Profile(); got != config.ProfileSlowCarrier {
		t.Fatalf("Profile() = %q, want %q", got, config.ProfileSlowCarrier)
	}
}

func TestEngine_HealthyAlwaysSucceeds(t *testing.T) {
	t.Parallel()

	seed := uint64(1)
	e := scenario.New(config.ScenarioConfig{Profile: config.ProfileHealthy, Latency: fixedLatency(20)}, nil)
	st := e.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 100; tick++ {
		if got := e.Evaluate(st, tick); got.Outcome != scenario.OutcomeSuccess || got.LatencyMS != 20 {
			t.Fatalf("Evaluate(%d) = %+v, want success at 20ms", tick, got)
		}
	}
}

func TestEngine_SlowCarrierBoundedNoError(t *testing.T) {
	t.Parallel()

	seed := uint64(9)
	e := scenario.New(config.ScenarioConfig{Profile: config.ProfileSlowCarrier, Latency: uniformLatency(2000, 4000)}, nil)
	st := e.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 1000; tick++ {
		got := e.Evaluate(st, tick)
		if got.Outcome != scenario.OutcomeSuccess {
			t.Fatalf("slow-carrier tick %d outcome = %v, want success", tick, got.Outcome)
		}
		if got.LatencyMS < 2000 || got.LatencyMS > 4000 {
			t.Fatalf("slow-carrier latency %d out of [2000,4000]", got.LatencyMS)
		}
	}
}

func TestEngine_DeadCarrierTimeoutAll(t *testing.T) {
	t.Parallel()

	mode := config.DeadCarrierTimeoutAll
	seed := uint64(3)
	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileDeadCarrier,
		Params:  config.ScenarioParams{Mode: &mode},
		Latency: fixedLatency(0),
	}, nil)
	if e.RejectBind() {
		t.Fatalf("timeout_all must not reject binds")
	}
	st := e.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 50; tick++ {
		if got := e.Evaluate(st, tick); got.Outcome != scenario.OutcomeTimeout {
			t.Fatalf("dead-carrier tick %d outcome = %v, want timeout", tick, got.Outcome)
		}
	}
}

func TestEngine_DeadCarrierRejectBind(t *testing.T) {
	t.Parallel()

	mode := config.DeadCarrierRejectBind
	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileDeadCarrier,
		Params:  config.ScenarioParams{Mode: &mode},
		Latency: fixedLatency(0),
	}, nil)
	if !e.RejectBind() {
		t.Fatalf("reject_bind must refuse binds")
	}
}

func TestEngine_ThrottlingOverCap(t *testing.T) {
	t.Parallel()

	// cap=1 with a real wall clock: the whole burst runs in well under a second, so the
	// first submit is allowed and the rest are throttled with the configured error_code.
	code := config.ErrorCodeRSysErr
	seed := uint64(5)
	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileThrottlingCarrier,
		Params:  config.ScenarioParams{ThroughputCapPerSec: i(1), ErrorCode: &code},
		Latency: fixedLatency(40),
	}, nil)
	st := e.NewBindState(&seed, "smsc", 1)

	if got := e.Evaluate(st, 1); got.Outcome != scenario.OutcomeSuccess {
		t.Fatalf("first submit under cap = %v, want success", got.Outcome)
	}
	for tick := uint64(2); tick <= 6; tick++ {
		got := e.Evaluate(st, tick)
		if got.Outcome != scenario.OutcomeError || got.Status != smpp.StatusSysErr {
			t.Fatalf("over-cap submit %d = %+v, want error ESME_RSYSERR", tick, got)
		}
	}
}

func TestEngine_ThroughputCappedOverCap(t *testing.T) {
	t.Parallel()

	seed := uint64(6)
	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileThroughputCapped,
		Params:  config.ScenarioParams{ThroughputCapPerSec: i(1)},
		Latency: fixedLatency(30),
	}, nil)
	st := e.NewBindState(&seed, "smsc", 1)

	if got := e.Evaluate(st, 1); got.Outcome != scenario.OutcomeSuccess {
		t.Fatalf("first submit under cap = %v, want success", got.Outcome)
	}
	for tick := uint64(2); tick <= 6; tick++ {
		got := e.Evaluate(st, tick)
		if got.Outcome != scenario.OutcomeError || got.Status != smpp.StatusThrottled {
			t.Fatalf("over-cap submit %d = %+v, want error ESME_RTHROTTLED", tick, got)
		}
	}
}

func TestEngine_FlakyStatisticalMix(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate: f64(0.8),
			ErrorMix:    map[config.SMPPErrorCode]uint{config.ErrorCodeRSysErr: 1, config.ErrorCodeRSubmitFail: 1},
		},
		Latency: fixedLatency(60),
	}, nil)

	seed := uint64(7)
	st := e.NewBindState(&seed, "smsc", 1)
	const n = 10_000
	var success int
	for tick := uint64(1); tick <= n; tick++ {
		got := e.Evaluate(st, tick)
		switch got.Outcome {
		case scenario.OutcomeSuccess:
			success++
		case scenario.OutcomeError:
			if got.Status != smpp.StatusSysErr && got.Status != smpp.StatusSubmitFail {
				t.Fatalf("flaky error status %d not from configured mix", got.Status)
			}
		default:
			t.Fatalf("flaky tick %d unexpected outcome %v", tick, got.Outcome)
		}
	}
	// Seed is fixed, so this is a deterministic assertion, not a real probability; the
	// band is generous around the 0.8 success_rate.
	ratio := float64(success) / n
	if ratio < 0.77 || ratio > 0.83 {
		t.Fatalf("flaky success ratio %.3f outside [0.77,0.83]", ratio)
	}
}

func TestEngine_FlakyDisconnectInterval(t *testing.T) {
	t.Parallel()

	e := scenario.New(config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate:             f64(1.0), // isolate the disconnect overlay from the error draw
			DisconnectIntervalTicks: u64(10),
		},
		Latency: fixedLatency(5),
	}, nil)

	seed := uint64(11)
	st := e.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 50; tick++ {
		got := e.Evaluate(st, tick)
		wantDisc := tick%10 == 0
		if wantDisc {
			if got.Outcome != scenario.OutcomeDisconnect || got.DisconnectWhen != config.DisconnectAfterResponse {
				t.Fatalf("tick %d = %+v, want disconnect after_response", tick, got)
			}
		} else if got.Outcome != scenario.OutcomeSuccess {
			t.Fatalf("tick %d = %+v, want success", tick, got)
		}
	}
}

func TestEngine_ErrorMixMapOrderIndependent(t *testing.T) {
	t.Parallel()

	cfg := config.ScenarioConfig{
		Profile: config.ProfileFlakyCarrier,
		Params: config.ScenarioParams{
			SuccessRate: f64(0.5),
			ErrorMix: map[config.SMPPErrorCode]uint{
				config.ErrorCodeRSysErr:     1,
				config.ErrorCodeRSubmitFail: 2,
				config.ErrorCodeRInvDstAdr:  3,
			},
		},
		Latency: fixedLatency(10),
	}
	// Two engines built from the same config must flatten error_mix identically despite
	// the map's randomised iteration order, so a shared seed yields identical decisions.
	seed := uint64(99)
	e1, e2 := scenario.New(cfg, nil), scenario.New(cfg, nil)
	st1, st2 := e1.NewBindState(&seed, "smsc", 1), e2.NewBindState(&seed, "smsc", 1)
	for tick := uint64(1); tick <= 500; tick++ {
		if e1.Evaluate(st1, tick) != e2.Evaluate(st2, tick) {
			t.Fatalf("error_mix flattening leaked map order at tick %d", tick)
		}
	}
}
