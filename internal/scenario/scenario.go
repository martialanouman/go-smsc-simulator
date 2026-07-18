// Package scenario is the decision engine a virtual SMSC consults on each
// submit_sm: given the active profile, what outcome and what served latency?
//
// At S2 only the healthy profile is implemented — 100% success at a fixed, low
// latency. The five other profiles are wired as marked STUBs that fall back to
// healthy so the end-to-end skeleton behaves deterministically; weighted outcomes,
// the fault injector and the seeded PRNG land at S3 (plan §7).
package scenario

import "github.com/martialanouman/go-smsc-simulator/internal/config"

// Outcome is what the engine decides to do with a submit_sm. Only Success exists at
// S2; Error/Timeout/Disconnect arrive with the fault injector at S3.
type Outcome int

// Outcome values.
const (
	// OutcomeSuccess accepts the submit_sm and returns ESME_ROK.
	OutcomeSuccess Outcome = iota
)

// Decision is the engine's verdict for one submit_sm: the outcome plus the latency
// (milliseconds) to serve before responding.
type Decision struct {
	Outcome   Outcome
	LatencyMS uint64
}

// Engine evaluates one virtual SMSC's active scenario. It is created once at boot
// from the immutable config and consulted per submit_sm; it holds no mutable state
// at S2, so it is safe to share across a virtual SMSC's bind sessions.
type Engine struct {
	profile   config.Profile
	latencyMS uint64
}

// New builds the engine for a scenario config. Every profile resolves to the same
// deterministic success behaviour at S2; the profile is retained only so the STUB
// boundary is explicit and so S3 can branch on it without a signature change.
func New(cfg config.ScenarioConfig) *Engine {
	return &Engine{
		profile:   cfg.Profile,
		latencyMS: fixedLatencyMS(cfg.Latency),
	}
}

// Evaluate returns the decision for the submit_sm at perBindClock. healthy is
// clock-independent, so perBindClock is unused at S2; it is part of the signature
// because S3's weighted, seeded selection is anchored on it (plan §1.5, §7).
func (e *Engine) Evaluate(perBindClock uint64) Decision {
	_ = perBindClock // STUB S3: weighted outcome selection keyed on (seed, per_bind_clock). See plan §7.

	switch e.profile {
	case config.ProfileHealthy:
		return Decision{Outcome: OutcomeSuccess, LatencyMS: e.latencyMS}
	default:
		// STUB S3: flaky/throttling/dead/slow/throughput-capped fall back to healthy
		// until the scenario engine lands. See plan §7.
		return Decision{Outcome: OutcomeSuccess, LatencyMS: e.latencyMS}
	}
}

// fixedLatencyMS reads a fixed-distribution latency. Only the fixed distribution is
// honoured at S2; uniform/normal/spike are served as zero until the fault injector
// implements them at S3 (plan §7).
func fixedLatencyMS(cfg config.LatencyConfig) uint64 {
	if cfg.Distribution == config.LatencyFixed && cfg.Params.MS != nil {
		return *cfg.Params.MS
	}
	return 0
}
