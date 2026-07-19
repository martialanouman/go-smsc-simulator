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
// use on OutcomeError, the latency (ms) to serve before responding, on OutcomeDisconnect
// whether to answer before dropping the link, and — only when the outcome is success and
// the profile configures DLRs — the DLR to schedule.
type Decision struct {
	Outcome        Outcome
	Status         smpp.CommandStatus
	LatencyMS      uint64
	DisconnectWhen config.DisconnectWhen
	DLR            *DLRPlan      // non-nil only on a successful submit under a DLR-enabled profile
	EdgeCase       *EdgeCasePlan // non-nil when the response should be malformed (protocol_edge_cases)
}

// EdgeCasePlan is the malformation to stamp on this submit's response when
// protocol_edge_cases is enabled: the session emits the submit_sm_resp via
// smpp.EncodeEdgeCase(kind) instead of the strict encoder. Injection is a pure tick
// function, so it never draws from the bind PRNG (invariant a).
type EdgeCasePlan struct {
	Kind smpp.EdgeCaseKind
}

// DLROutcome is the resolved state of a scheduled delivery receipt.
type DLROutcome int

// DLROutcome values, mirroring the outcome_weights knobs (spec §3.1).
const (
	// DLRDelivered reports the message delivered (stat:DELIVRD, message_state DELIVERED).
	DLRDelivered DLROutcome = iota
	// DLRFailed reports the message undeliverable (stat:UNDELIV, message_state UNDELIVERABLE).
	DLRFailed
	// DLRExpired reports the message expired (stat:EXPIRED, message_state EXPIRED).
	DLRExpired
)

// String renders the outcome as the lowercase weight name, for logs and assertions.
func (o DLROutcome) String() string {
	switch o {
	case DLRDelivered:
		return "delivered"
	case DLRFailed:
		return "failed"
	case DLRExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// DLRPlan is the scheduled delivery receipt for a successful submit: the resolved
// outcome and the tick offset (from the origin submit's per_bind_clock) at which the
// deliver_sm should fire. The delay is fixed ticks at S4 (uniform reserved).
type DLRPlan struct {
	Outcome    DLROutcome
	DelayTicks uint64
}

// weightedDLR is one entry of the pre-flattened outcome_weights: an outcome and the
// running cumulative weight up to and including it, so a single PRNG draw selects it.
type weightedDLR struct {
	outcome DLROutcome
	cum     uint64
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

	// DLR generation, nil/zero when scenario.dlr is absent.
	dlrEnabled    bool
	dlrMix        []weightedDLR // pre-flattened outcome_weights; deterministic weighted pick
	dlrDelayTicks uint64        // fixed tick offset from the origin submit

	// Protocol edge-case injection (protocol_edge_cases), off unless the master switch is
	// set. Cadence and kind rotation are pure tick functions — no PRNG draw — so enabling
	// them never perturbs the seeded decision stream (invariant a).
	edgeEnabled    bool
	edgeEveryTicks uint64              // inject on ticks where t % edgeEveryTicks == 0
	edgeKinds      []smpp.EdgeCaseKind // rotated in order across injecting ticks
}

// BindState is the per-bind mutable state: the seeded PRNG and, when the profile or a
// throughput limit demands it, the wall-clock throughput gate. It is created at bind
// time and owned by the session's read goroutine — never shared, never locked (same
// ownership rule as the SMPP session state).
type BindState struct {
	rng       *rand.Rand
	schedBase uint64            // base for idempotent schedule coins (scope: random disconnect)
	gate      *fault.WallWindow // nil unless a throughput cap applies
}

// New builds the engine for a scenario config. limitPerSec is the vSMSC-level
// throughput_limit_per_sec (nil when unset), enforced independently of the profile.
func New(cfg config.ScenarioConfig, limitPerSec *int) *Engine {
	e := &Engine{
		profile:     cfg.Profile,
		params:      cfg.Params,
		latency:     cfg.Latency,
		errorMix:    buildErrorMix(cfg.Params.ErrorMix),
		limitPerSec: limitPerSec,
	}
	if cfg.DLR != nil {
		e.dlrEnabled = true
		e.dlrMix = buildDLRMix(cfg.DLR.OutcomeWeights)
		if cfg.DLR.Delay.Ticks != nil { // validation guarantees this under the fixed delay
			e.dlrDelayTicks = *cfg.DLR.Delay.Ticks
		}
	}
	if cfg.ProtocolEdgeCasesEnabled {
		e.edgeEnabled = true
		e.edgeEveryTicks = 1
		e.edgeKinds = allEdgeKinds()
		if ec := cfg.ProtocolEdgeCases; ec != nil {
			if ec.InjectEveryTicks != nil { // validation guarantees >= 1
				e.edgeEveryTicks = *ec.InjectEveryTicks
			}
			if len(ec.Kinds) > 0 { // empty list keeps the all-kinds default
				e.edgeKinds = mapEdgeKinds(ec.Kinds)
			}
		}
	}
	return e
}

// allEdgeKinds is the default rotation when protocol_edge_cases is enabled without an
// explicit kinds list: every malformation, in a fixed order.
func allEdgeKinds() []smpp.EdgeCaseKind {
	return []smpp.EdgeCaseKind{smpp.EdgeBadLength, smpp.EdgeUnknownCmdID, smpp.EdgeBadSequence}
}

// mapEdgeKinds translates the config's string kinds onto the codec's kinds, preserving
// order (the rotation is order-sensitive). Validation has already rejected any unknown
// kind, so the default arm is unreachable in practice.
func mapEdgeKinds(kinds []config.EdgeCaseKind) []smpp.EdgeCaseKind {
	out := make([]smpp.EdgeCaseKind, 0, len(kinds))
	for _, k := range kinds {
		switch k {
		case config.EdgeCaseBadLength:
			out = append(out, smpp.EdgeBadLength)
		case config.EdgeCaseUnknownCmdID:
			out = append(out, smpp.EdgeUnknownCmdID)
		case config.EdgeCaseBadSequence:
			out = append(out, smpp.EdgeBadSequence)
		}
	}
	return out
}

// Profile returns the catalogue profile this engine evaluates. A session reads it to
// label metrics by the profile currently active on its bind (a transition swaps the
// engine), so the label always follows this bind rather than a SMSC-global observable.
func (e *Engine) Profile() config.Profile {
	return e.profile
}

// NewBindState creates the per-bind state for a newly bound session. With a seed the
// PRNG is derived deterministically from (seed, smscID, bindOrdinal); without one it
// is a chaos-mode source. A throughput gate is attached only when a cap applies.
func (e *Engine) NewBindState(seed *uint64, smscID string, bindOrdinal uint64) *BindState {
	st := &BindState{}
	if seed != nil {
		st.rng = rng.NewBind(*seed, smscID, bindOrdinal)
		st.schedBase = rng.ScheduleBase(*seed, smscID, bindOrdinal)
	} else {
		st.rng = rng.NewChaos()
		st.schedBase = rand.Uint64() // chaos: fixed per bind, not reproducible across runs
	}
	if cap, ok := e.effectiveCap(); ok {
		st.gate = fault.NewWallWindow(cap)
	}
	return st
}

// DisconnectDraw returns the coin for a scope: random scheduled disconnect at tick — whether
// THIS bind cuts itself. It is a pure, idempotent function of the bind's schedule base and
// the tick, so peeking it before a submit's response and re-evaluating it when the event
// drains always agree, and it never perturbs the scenario decision stream (invariant a). A
// bind can only ever close itself (session state is single-goroutine-owned), so random
// scope is a per-bind ~50% decision, not a coordinated pick of one bind among many.
func (st *BindState) DisconnectDraw(tick uint64) bool {
	return rng.ScheduleCoin(st.schedBase, tick)
}

// RejectBind reports whether the active profile refuses binds outright — true only
// for dead-carrier in reject_bind mode. The session consults it before authenticating
// (a dead carrier turns everyone away).
func (e *Engine) RejectBind() bool {
	return e.profile == config.ProfileDeadCarrier &&
		e.params.Mode != nil && *e.params.Mode == config.DeadCarrierRejectBind
}

// Evaluate returns the decision for the submit_sm at tick t on the given bind. On a
// successful submit under a DLR-enabled profile it also resolves the DLR to schedule,
// drawing its outcome HERE — as the last draw of the tick, a fixed position that keeps
// the seeded replay total (invariant a). Error/timeout/disconnect never draw a DLR.
func (e *Engine) Evaluate(st *BindState, t uint64) Decision {
	d := e.evaluate(st, t)
	if e.dlrEnabled && d.Outcome == OutcomeSuccess {
		d.DLR = &DLRPlan{Outcome: e.pickDLROutcome(st.rng), DelayTicks: e.dlrDelayTicks}
	}
	// Edge-case injection is decided last and consumes no PRNG draw, so it cannot shift
	// the DLR/error stream above it — enabling it leaves a seeded replay byte-identical.
	if k, ok := e.edgeCaseFor(t); ok {
		d.EdgeCase = &EdgeCasePlan{Kind: k}
	}
	return d
}

// edgeCaseFor returns the malformation to stamp on tick t's response, if injection is
// enabled and the cadence fires. It is a pure function of the tick: t % edgeEveryTicks
// selects the injecting ticks, and (t/edgeEveryTicks - 1) rotates through edgeKinds in
// order. No wall clock, no PRNG — reproducible per bind on replay (invariant a).
//
// The t==0, edgeEveryTicks!=0 and len!=0 conditions are guards, not dead checks: submit
// ticks are perBindClock+1 so they start at 1, but a t of 0 would underflow the rotation
// index, and a zero cadence or empty kinds list would divide by zero in the modulo. New()
// upholds all three today; the guards keep this total for any future caller.
func (e *Engine) edgeCaseFor(t uint64) (smpp.EdgeCaseKind, bool) {
	if !e.edgeEnabled || t == 0 || len(e.edgeKinds) == 0 || e.edgeEveryTicks == 0 || t%e.edgeEveryTicks != 0 {
		return 0, false
	}
	idx := (t/e.edgeEveryTicks - 1) % uint64(len(e.edgeKinds))
	return e.edgeKinds[idx], true
}

// evaluate resolves the submit outcome and served latency for tick t, before any DLR is
// planned. Split from Evaluate so the DLR draw stays a single, well-defined last step.
func (e *Engine) evaluate(st *BindState, t uint64) Decision {
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

// buildDLRMix flattens the delivered/failed/expired weights into a cumulative slice in
// a fixed order (delivered, failed, expired), so a single PRNG draw selects an outcome
// reproducibly. Unlike error_mix there is no map to defend against — the weights are a
// fixed struct — but the cumulative form keeps the pick a single draw. Zero-weight
// outcomes are dropped so they can never be selected.
func buildDLRMix(w config.DLROutcomeWeights) []weightedDLR {
	entries := []struct {
		outcome DLROutcome
		weight  uint
	}{
		{DLRDelivered, w.Delivered},
		{DLRFailed, w.Failed},
		{DLRExpired, w.Expired},
	}
	out := make([]weightedDLR, 0, len(entries))
	var cum uint64
	for _, en := range entries {
		if en.weight == 0 {
			continue
		}
		cum += uint64(en.weight)
		out = append(out, weightedDLR{outcome: en.outcome, cum: cum})
	}
	return out
}

// pickDLROutcome draws one weighted DLR outcome from the pre-flattened outcome_weights.
func (e *Engine) pickDLROutcome(r *rand.Rand) DLROutcome {
	if len(e.dlrMix) == 0 {
		return DLRDelivered // validated non-zero sum makes this unreachable: defensive
	}
	total := e.dlrMix[len(e.dlrMix)-1].cum
	n := r.Uint64N(total)
	for _, w := range e.dlrMix {
		if n < w.cum {
			return w.outcome
		}
	}
	return e.dlrMix[len(e.dlrMix)-1].outcome // unreachable: n < total always matches
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
