package config

import "log/slog"

// This file models the full §3.1 .yml schema as an immutable struct tree. Every
// struct carries yaml tags in snake_case aligned exactly on the spec; optional
// fields are pointers so "absent" stays distinguishable from a real zero value
// (convention §3). The decoder runs with KnownFields(true) (config.go), so an
// unknown key anywhere in this tree is rejected as a typo — except inside map
// fields, which yaml does not police (see ErrorMix).

// VirtualSMSCConfig describes one virtual SMSC: its listen port, bind credentials,
// deterministic seed, active scenario and the three declarative forms of scheduled
// events. It is the immutable reflection of one virtual_smscs[] entry.
type VirtualSMSCConfig struct {
	Name                  string                `yaml:"name"`
	Port                  int                   `yaml:"port"`
	BindCredentials       BindCredentials       `yaml:"bind_credentials"`
	AddrTON               int                   `yaml:"addr_ton"`
	AddrNPI               int                   `yaml:"addr_npi"`
	AddressRange          string                `yaml:"address_range"`
	TLS                   TLSConfig             `yaml:"tls"`
	Seed                  *uint64               `yaml:"seed"` // nil = chaos/unseeded mode
	PDUBufferSize         int                   `yaml:"pdu_buffer_size"`
	ThroughputLimitPerSec *int                  `yaml:"throughput_limit_per_sec"` // nil = no limit
	QuiescenceFlushMs     *uint64               `yaml:"quiescence_flush_ms"`      // nil = default (QuiescenceFlushDefaultMs)
	Scenario              ScenarioConfig        `yaml:"scenario"`
	MOInjection           *MOInjectionConfig    `yaml:"mo_injection"` // nil = no MO injection
	ScheduledDisconnects  []ScheduledDisconnect `yaml:"scheduled_disconnects"`
	ScheduledTransitions  []ScheduledTransition `yaml:"scheduled_transitions"`
}

// QuiescenceFlushDefaultMs is the default idle window, in milliseconds, after which a
// bind's pending tick-scheduled events (DLRs at S4; MO/disconnects/transitions at S5)
// are flushed even though no new submit_sm has advanced the clock (spec §6.3, invariant
// d). Overridable per virtual SMSC via quiescence_flush_ms.
const QuiescenceFlushDefaultMs uint64 = 250

// EffectiveQuiescenceFlushMs returns the configured quiescence-flush window, or the
// default when quiescence_flush_ms is absent. Kept here so the default lives in one place
// and Config stays pure data (the caller converts the millisecond count to a duration).
func (vs VirtualSMSCConfig) EffectiveQuiescenceFlushMs() uint64 {
	if vs.QuiescenceFlushMs != nil {
		return *vs.QuiescenceFlushMs
	}
	return QuiescenceFlushDefaultMs
}

// BindCredentials is the system_id/password a client must present to bind. The
// password is a secret: it is read from the .yml but never serialized by the
// read-only surface (spec §5.2). LogValue redacts it so an accidental structured
// log cannot spill it either.
type BindCredentials struct {
	SystemID string `yaml:"system_id"`
	Password string `yaml:"password"`
}

// LogValue redacts the bind secret so structured logs can never spill it.
func (b BindCredentials) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("system_id", b.SystemID),
		slog.String("password", "REDACTED"),
	)
}

// TLSConfig toggles TLS on a virtual SMSC's listener. Only the enabled flag is
// modelled at S1; certificate fields (auto-signed generation) land at S6.
type TLSConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ScenarioConfig selects the active profile and its exposed knobs, the served
// latency, and optional DLR generation. The profile must belong to the frozen
// catalogue (spec §6.1); params may only set the knobs that profile exposes.
type ScenarioConfig struct {
	Profile                  Profile        `yaml:"profile"`
	Params                   ScenarioParams `yaml:"params"`
	Latency                  LatencyConfig  `yaml:"latency"`
	DLR                      *DLRConfig     `yaml:"dlr"` // nil = no DLR generation
	ProtocolEdgeCasesEnabled bool           `yaml:"protocol_edge_cases_enabled"`
}

// ScenarioParams is the union of every knob any profile exposes. KnownFields
// accepts any of these keys structurally, so validate() enforces that only the
// knobs the active profile exposes are set (spec §3.1: "only the knobs the chosen
// profile exposes"). Pointers distinguish absent from a real zero — that nil/non-nil
// signal is exactly what the exposed-knob check reads.
type ScenarioParams struct {
	SuccessRate             *float64               `yaml:"success_rate"`              // flaky-carrier
	ErrorMix                map[SMPPErrorCode]uint `yaml:"error_mix"`                 // flaky-carrier
	DisconnectIntervalTicks *uint64                `yaml:"disconnect_interval_ticks"` // flaky-carrier
	ThroughputCapPerSec     *int                   `yaml:"throughput_cap_per_sec"`    // throttling / throughput-capped
	ErrorCode               *SMPPErrorCode         `yaml:"error_code"`                // throttling-carrier
	Mode                    *DeadCarrierMode       `yaml:"mode"`                      // dead-carrier
}

// LatencyConfig picks a distribution and its params. The valid params keys depend
// on the distribution (validated at load): fixed{ms}, uniform{min_ms,max_ms},
// normal{mean_ms,stddev_ms}, spike{base_ms,spike_ms,interval_ticks}.
type LatencyConfig struct {
	Distribution LatencyDistribution `yaml:"distribution"`
	Params       LatencyParams       `yaml:"params"`
}

// LatencyParams is the union of every distribution's params keys. As with
// ScenarioParams, pointers distinguish absent from zero and validate() enforces
// that only the keys the chosen distribution uses are set.
type LatencyParams struct {
	MS            *uint64 `yaml:"ms"`             // fixed
	MinMS         *uint64 `yaml:"min_ms"`         // uniform
	MaxMS         *uint64 `yaml:"max_ms"`         // uniform
	MeanMS        *uint64 `yaml:"mean_ms"`        // normal
	StddevMS      *uint64 `yaml:"stddev_ms"`      // normal
	BaseMS        *uint64 `yaml:"base_ms"`        // spike
	SpikeMS       *uint64 `yaml:"spike_ms"`       // spike
	IntervalTicks *uint64 `yaml:"interval_ticks"` // spike; interval in ticks (per_bind_clock)
}

// DLRConfig describes asynchronous delivery-receipt generation for successfully
// submitted messages (spec §3.1). clock: wallclock is only valid without a seed.
type DLRConfig struct {
	Delay          DLRDelay          `yaml:"delay"`
	OutcomeWeights DLROutcomeWeights `yaml:"outcome_weights"`
	Clock          Clock             `yaml:"clock"`
}

// DLRDelay anchors the DLR to a tick offset from the origin submit_sm. S1 supports
// the fixed distribution (ticks); uniform bounds are reserved for a later milestone.
type DLRDelay struct {
	Distribution LatencyDistribution `yaml:"distribution"`
	Ticks        *uint64             `yaml:"ticks"`     // fixed
	MinTicks     *uint64             `yaml:"min_ticks"` // uniform (reserved)
	MaxTicks     *uint64             `yaml:"max_ticks"` // uniform (reserved)
}

// DLROutcomeWeights is the weighted mix of DLR outcomes (spec §3.1). Weights are
// non-negative integers; their sum must be at least one so a DLR always resolves.
type DLROutcomeWeights struct {
	Delivered uint `yaml:"delivered"`
	Failed    uint `yaml:"failed"`
	Expired   uint `yaml:"expired"`
}

// MOInjectionConfig declares unsolicited deliver_sm injection (spec §3.1). In
// scheduled mode Events drives it; in auto mode RatePerSec/ContentTemplate do.
type MOInjectionConfig struct {
	Mode            MOMode    `yaml:"mode"`
	Clock           Clock     `yaml:"clock"`
	Events          []MOEvent `yaml:"events"`           // mode: scheduled
	RatePerSec      *int      `yaml:"rate_per_sec"`     // mode: auto
	ContentTemplate *string   `yaml:"content_template"` // mode: auto
}

// MOEvent is one tick-anchored mobile-originated message (spec §3.1).
type MOEvent struct {
	AtTick     uint64 `yaml:"at_tick"`
	SourceAddr string `yaml:"source_addr"`
	DestAddr   string `yaml:"dest_addr"`
	Content    string `yaml:"content"`
}

// ScheduledDisconnect cuts binds at a tick (spec §3.1): which binds (scope) and
// when relative to the response (when).
type ScheduledDisconnect struct {
	AtTick uint64          `yaml:"at_tick"`
	Scope  DisconnectScope `yaml:"scope"`
	When   DisconnectWhen  `yaml:"when"`
}

// ScheduledTransition advances the active profile at a tick (spec §3.1, §6.1). This
// is the ONLY way active_scenario moves — there is no runtime mutation path.
// to_profile must reference a known profile.
type ScheduledTransition struct {
	AtTick    uint64  `yaml:"at_tick"`
	ToProfile Profile `yaml:"to_profile"`
}
