// Package scenario is the decision engine a virtual SMSC consults on each submit_sm:
// given the active profile and the bind's seeded PRNG, what outcome and what served
// latency? The six profiles of the frozen catalogue (spec §6.1) live here as a
// BEHAVIOURAL catalogue, distinct from the VALIDATION catalogue in package config.
//
// Determinism (invariant a) is scoped per bind: every random decision draws from the
// bind's own PRNG in a fixed, documented order anchored to per_bind_clock, so a
// seeded replay of a fixture reproduces the same outcomes and latencies. The one
// exception is the throughput cap, which is a real-time wall-clock mechanism (spec
// §6.2/§6.3): throttling-carrier / throughput-capped binds are asserted by threshold,
// not byte-for-byte replay.
package scenario

import (
	"math/rand/v2"
	"sort"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/fault"
	"github.com/martialanouman/go-smsc-simulator/internal/rng"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// Outcome is what the engine decides to do with a submit_sm.
type Outcome int

// Outcome values.
const (
	// OutcomeSuccess accepts the submit_sm and returns ESME_ROK.
	OutcomeSuccess Outcome = iota
	// OutcomeError returns a non-ROK submit_sm_resp carrying Decision.Status.
	OutcomeError
	// OutcomeTimeout withholds the submit_sm_resp entirely (the carrier hangs).
	OutcomeTimeout
	// OutcomeDisconnect drops the TCP connection, per Decision.DisconnectWhen.
	OutcomeDisconnect
)

// Decision is the engine's verdict for one submit_sm: the outcome, the wire status to
// use on OutcomeError, the latency (ms) to serve before responding, and — on
// OutcomeDisconnect — whether to answer before dropping the link.
type Decision struct {
	Outcome        Outcome
	Status         smpp.CommandStatus
	LatencyMS      uint64
	DisconnectWhen config.DisconnectWhen
}

// weightedCode is one entry of the pre-flattened error_mix: a wire status and the
// running cumulative weight up to and including it, so a single PRNG draw selects a
// code in O(log n)/O(n) without touching the (randomly-ordered) source map.
type weightedCode struct {
	status smpp.CommandStatus
	cum    uint64
}

// Engine evaluates one virtual SMSC's active scenario. It is built once at boot from
// the immutable config and holds no mutable state, so it is safe to share across a
// virtual SMSC's bind sessions; all per-bind mutable state lives in BindState.
type Engine struct {
	profile     config.Profile
	params      config.ScenarioParams
	latency     config.LatencyConfig
	errorMix    []weightedCode // pre-flattened + sorted; deterministic weighted pick
	limitPerSec *int           // vSMSC throughput_limit_per_sec, enforced independently
}

// BindState is the per-bind mutable state: the seeded PRNG and, when the profile or a
// throughput limit demands it, the wall-clock throughput gate. It is created at bind
// time and owned by the session's read goroutine — never shared, never locked (same
// ownership rule as the SMPP session state).
type BindState struct {
	rng  *rand.Rand
	gate *fault.WallWindow // nil unless a throughput cap applies
}

// New builds the engine for a scenario config. limitPerSec is the vSMSC-level
// throughput_limit_per_sec (nil when unset), enforced independently of the profile.
func New(cfg config.ScenarioConfig, limitPerSec *int) *Engine {
	return &Engine{
		profile:     cfg.Profile,
		params:      cfg.Params,
		latency:     cfg.Latency,
		errorMix:    buildErrorMix(cfg.Params.ErrorMix),
		limitPerSec: limitPerSec,
	}
}

// NewBindState creates the per-bind state for a newly bound session. With a seed the
// PRNG is derived deterministically from (seed, smscID, bindOrdinal); without one it
// is a chaos-mode source. A throughput gate is attached only when a cap applies.
func (e *Engine) NewBindState(seed *uint64, smscID string, bindOrdinal uint64) *BindState {
	st := &BindState{}
	if seed != nil {
		st.rng = rng.NewBind(*seed, smscID, bindOrdinal)
	} else {
		st.rng = rng.NewChaos()
	}
	if cap, ok := e.effectiveCap(); ok {
		st.gate = fault.NewWallWindow(cap)
	}
	return st
}

// RejectBind reports whether the active profile refuses binds outright — true only
// for dead-carrier in reject_bind mode. The session consults it before authenticating
// (a dead carrier turns everyone away).
func (e *Engine) RejectBind() bool {
	return e.profile == config.ProfileDeadCarrier &&
		e.params.Mode != nil && *e.params.Mode == config.DeadCarrierRejectBind
}

// Evaluate returns the decision for the submit_sm at tick t on the given bind.
func (e *Engine) Evaluate(st *BindState, t uint64) Decision {
	// Throughput gate first — the one wall-clock, non-deterministic path. It fires for
	// throttling-carrier / throughput-capped (profile cap) and for any profile carrying
	// a vSMSC throughput_limit_per_sec. Profiles without a cap have gate==nil and skip
	// this entirely, so their draw discipline and replay determinism are preserved.
	if st.gate != nil && !st.gate.Allow() {
		return Decision{Outcome: OutcomeError, Status: e.throttleStatus()}
	}

	switch e.profile {
	case config.ProfileFlakyCarrier:
		return e.evaluateFlaky(st, t)

	case config.ProfileDeadCarrier:
		// reject_bind never reaches Evaluate (handled at bind); only timeout_all does.
		return Decision{Outcome: OutcomeTimeout}

	case config.ProfileHealthy, config.ProfileSlowCarrier,
		config.ProfileThrottlingCarrier, config.ProfileThroughputCapped:
		// Success under the cap (the gate above already handled over-cap); slow-carrier's
		// bounded latency comes straight from its uniform config.
		return Decision{Outcome: OutcomeSuccess, LatencyMS: e.latencyMS(st, t)}

	default:
		return Decision{Outcome: OutcomeSuccess, LatencyMS: e.latencyMS(st, t)}
	}
}

// evaluateFlaky implements flaky-carrier. The PRNG draw order is fixed and documented
// so a seeded replay reproduces every tick (anchored to perBindClock): (1) success vs
// error, (2) error-code pick on the error branch only, (3) latency. The periodic
// disconnect overlays the drawn outcome without consuming a draw, so the stream
// position stays a pure function of the tick.
func (e *Engine) evaluateFlaky(st *BindState, t uint64) Decision {
	successRate := 1.0
	if e.params.SuccessRate != nil {
		successRate = *e.params.SuccessRate
	}

	d := Decision{Outcome: OutcomeSuccess}
	if st.rng.Float64() >= successRate {
		d.Outcome = OutcomeError
		d.Status = e.pickErrorStatus(st.rng)
	}
	d.LatencyMS = e.latencyMS(st, t)

	if iv := e.params.DisconnectIntervalTicks; iv != nil && *iv != 0 && t%*iv == 0 {
		d.Outcome = OutcomeDisconnect
		d.DisconnectWhen = config.DisconnectAfterResponse
	}
	return d
}

// latencyMS draws the served latency from the bind's PRNG per the latency config.
func (e *Engine) latencyMS(st *BindState, t uint64) uint64 {
	return fault.LatencyMS(e.latency, t, st.rng)
}

// throttleStatus is the status returned when the throughput gate rejects a submit:
// throttling-carrier honours its configured error_code; every other capped path
// (throughput-capped, or a bare vSMSC limit) returns ESME_RTHROTTLED.
func (e *Engine) throttleStatus() smpp.CommandStatus {
	if e.profile == config.ProfileThrottlingCarrier && e.params.ErrorCode != nil {
		return statusFor(*e.params.ErrorCode)
	}
	return smpp.StatusThrottled
}

// pickErrorStatus draws one weighted error code from the pre-flattened error_mix.
func (e *Engine) pickErrorStatus(r *rand.Rand) smpp.CommandStatus {
	if len(e.errorMix) == 0 {
		return smpp.StatusSubmitFail // flaky error with no configured mix: defensive
	}
	total := e.errorMix[len(e.errorMix)-1].cum
	n := r.Uint64N(total)
	for _, wc := range e.errorMix {
		if n < wc.cum {
			return wc.status
		}
	}
	return e.errorMix[len(e.errorMix)-1].status // unreachable: n < total always matches
}

// effectiveCap returns the tightest throughput cap that applies to this profile, if
// any: the profile's own throughput_cap_per_sec (throttling / throughput-capped) and
// the vSMSC-level throughput_limit_per_sec, whichever are set.
func (e *Engine) effectiveCap() (int, bool) {
	cap, ok := 0, false
	consider := func(c int) {
		if !ok || c < cap {
			cap, ok = c, true
		}
	}
	if e.limitPerSec != nil {
		consider(*e.limitPerSec)
	}
	if e.profile == config.ProfileThrottlingCarrier || e.profile == config.ProfileThroughputCapped {
		if e.params.ThroughputCapPerSec != nil {
			consider(*e.params.ThroughputCapPerSec)
		}
	}
	return cap, ok
}

// buildErrorMix flattens the error_mix map into a sorted, cumulative-weight slice.
// The map's iteration order is randomised per run, so sorting by code string is what
// makes the weighted pick reproducible — without it, map order would leak into which
// code a given PRNG draw selects and break replay (invariant a).
func buildErrorMix(mix map[config.SMPPErrorCode]uint) []weightedCode {
	if len(mix) == 0 {
		return nil
	}
	codes := make([]config.SMPPErrorCode, 0, len(mix))
	for c := range mix {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })

	out := make([]weightedCode, 0, len(codes))
	var cum uint64
	for _, c := range codes {
		w := mix[c]
		if w == 0 {
			continue
		}
		cum += uint64(w)
		out = append(out, weightedCode{status: statusFor(c), cum: cum})
	}
	return out
}
