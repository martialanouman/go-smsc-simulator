package config

// This file holds every enumeration that appears in the .yml schema (spec §3.1).
//
// Each enum is a named string type whose constant values are EXACTLY the strings
// the operator writes in the .yml — a divergence would be a config-contract bug
// (convention §2.3). Every type exposes a Valid() method so validation stays
// centralised at config load (validate.go), never re-switched at a call site.

// Profile is one of the six fixed, predefined scenario profiles. The catalogue is
// frozen in code (spec §6.1): the .yml selects a profile by name and tunes only the
// knobs it exposes — it can never define arbitrary response rules.
type Profile string

// Profile values — exactly the strings written in scenario.profile.
const (
	ProfileHealthy           Profile = "healthy"
	ProfileFlakyCarrier      Profile = "flaky-carrier"
	ProfileThrottlingCarrier Profile = "throttling-carrier"
	ProfileDeadCarrier       Profile = "dead-carrier"
	ProfileSlowCarrier       Profile = "slow-carrier"
	ProfileThroughputCapped  Profile = "throughput-capped"
)

// Valid reports whether p is a known profile. Config loading rejects any unknown
// value (fail-fast: rejected at config load).
func (p Profile) Valid() bool {
	switch p {
	case ProfileHealthy, ProfileFlakyCarrier, ProfileThrottlingCarrier,
		ProfileDeadCarrier, ProfileSlowCarrier, ProfileThroughputCapped:
		return true
	default:
		return false
	}
}

// LatencyDistribution names the shape of the served latency (spec §6.2). Its
// params keys depend on the distribution; the mapping is enforced at load.
type LatencyDistribution string

// LatencyDistribution values.
const (
	LatencyFixed   LatencyDistribution = "fixed"
	LatencyUniform LatencyDistribution = "uniform"
	LatencyNormal  LatencyDistribution = "normal"
	LatencySpike   LatencyDistribution = "spike"
)

// Valid reports whether d is a known latency distribution.
func (d LatencyDistribution) Valid() bool {
	switch d {
	case LatencyFixed, LatencyUniform, LatencyNormal, LatencySpike:
		return true
	default:
		return false
	}
}

// Clock selects the timing reference of a scheduled mechanism. wallclock is only
// accepted when no seed is set (chaos mode); with a seed everything is anchored to
// per_bind_clock (spec §3.1, §6.3).
type Clock string

// Clock values.
const (
	ClockLogical   Clock = "logical"
	ClockWallclock Clock = "wallclock"
)

// Valid reports whether c is a known clock reference.
func (c Clock) Valid() bool {
	switch c {
	case ClockLogical, ClockWallclock:
		return true
	default:
		return false
	}
}

// DisconnectScope selects which binds a scheduled disconnect cuts (spec §3.1).
type DisconnectScope string

// DisconnectScope values.
const (
	DisconnectScopeAll    DisconnectScope = "all"
	DisconnectScopeOldest DisconnectScope = "oldest"
	DisconnectScopeRandom DisconnectScope = "random"
)

// Valid reports whether s is a known disconnect scope.
func (s DisconnectScope) Valid() bool {
	switch s {
	case DisconnectScopeAll, DisconnectScopeOldest, DisconnectScopeRandom:
		return true
	default:
		return false
	}
}

// DisconnectWhen selects the moment a scheduled disconnect fires relative to the
// submit_sm response (spec §3.1).
type DisconnectWhen string

// DisconnectWhen values.
const (
	DisconnectBeforeResponse DisconnectWhen = "before_response"
	DisconnectAfterResponse  DisconnectWhen = "after_response"
)

// Valid reports whether w is a known disconnect timing.
func (w DisconnectWhen) Valid() bool {
	switch w {
	case DisconnectBeforeResponse, DisconnectAfterResponse:
		return true
	default:
		return false
	}
}

// MOMode selects how mobile-originated messages are injected (spec §3.1): a fixed
// list of tick-anchored events, an auto rate, or none.
type MOMode string

// MOMode values.
const (
	MOModeScheduled MOMode = "scheduled"
	MOModeAuto      MOMode = "auto"
	MOModeDisabled  MOMode = "disabled"
)

// Valid reports whether m is a known MO injection mode.
func (m MOMode) Valid() bool {
	switch m {
	case MOModeScheduled, MOModeAuto, MOModeDisabled:
		return true
	default:
		return false
	}
}

// DeadCarrierMode is the exposed knob of the dead-carrier profile (spec §6.1): the
// carrier either refuses binds outright or accepts them and times out on every
// submit_sm.
type DeadCarrierMode string

// DeadCarrierMode values.
const (
	DeadCarrierRejectBind DeadCarrierMode = "reject_bind"
	DeadCarrierTimeoutAll DeadCarrierMode = "timeout_all"
)

// Valid reports whether m is a known dead-carrier mode.
func (m DeadCarrierMode) Valid() bool {
	switch m {
	case DeadCarrierRejectBind, DeadCarrierTimeoutAll:
		return true
	default:
		return false
	}
}

// SMPPErrorCode is an SMPP command status returned in place of a success, used by
// scenario.params.error_code (throttling-carrier) and the keys of error_mix
// (flaky-carrier). It is a named type so a typo is caught at load: error_mix is a
// map, and yaml's KnownFields does NOT police map keys, so SMPPErrorCode.Valid() is
// the only guard against an unknown status slipping through.
type SMPPErrorCode string

// SMPPErrorCode values — the curated known set; extend as scenarios need.
const (
	ErrorCodeROK         SMPPErrorCode = "ESME_ROK"
	ErrorCodeRThrottled  SMPPErrorCode = "ESME_RTHROTTLED"
	ErrorCodeRSubmitFail SMPPErrorCode = "ESME_RSUBMITFAIL"
	ErrorCodeRInvDstAdr  SMPPErrorCode = "ESME_RINVDSTADR"
	ErrorCodeRSysErr     SMPPErrorCode = "ESME_RSYSERR"
	ErrorCodeRMsgQFul    SMPPErrorCode = "ESME_RMSGQFUL"
	ErrorCodeRInvSrcAdr  SMPPErrorCode = "ESME_RINVSRCADR"
)

// Valid reports whether c is a known SMPP error code.
func (c SMPPErrorCode) Valid() bool {
	switch c {
	case ErrorCodeROK, ErrorCodeRThrottled, ErrorCodeRSubmitFail, ErrorCodeRInvDstAdr,
		ErrorCodeRSysErr, ErrorCodeRMsgQFul, ErrorCodeRInvSrcAdr:
		return true
	default:
		return false
	}
}
