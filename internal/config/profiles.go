package config

// This file is the profile VALIDATION catalogue: for each frozen profile (spec
// §6.1) it names the params knobs the profile exposes and the closed interval each
// numeric knob accepts. It answers the only question fail-fast must answer before a
// port opens — "is this knob legal for this profile, and is its value in range".
//
// It lives in package config, permanently, on purpose: config is imported by
// everyone, so it cannot import the future internal/scenario (that package will
// import config for the shared types — the reverse would be an import cycle). At S3
// internal/scenario builds the BEHAVIOURAL catalogue (weights, latency application,
// outcome selection); if it wants a single source of truth for the exposed-knob
// set, it consumes a read-only accessor from config rather than relocating this
// table. Do not move this catalogue out of config.

// Knob names, matching the scenario.params yaml keys. Used both to build each
// profile's exposed set and to name the offending field in validation errors.
const (
	knobSuccessRate             = "success_rate"
	knobErrorMix                = "error_mix"
	knobDisconnectIntervalTicks = "disconnect_interval_ticks"
	knobThroughputCapPerSec     = "throughput_cap_per_sec"
	knobErrorCode               = "error_code"
	knobMode                    = "mode"
)

// Numeric bounds. The spec pins only slow-carrier's 2–4 s latency (§6.1); the rest
// are conservative, documented choices that reject nonsense while leaving realistic
// load-test values room. Latency values are milliseconds.
const (
	throughputCapMin = 1
	throughputCapMax = 1_000_000

	latencyMSMax = 600_000 // 10 min ceiling on any served latency

	slowCarrierLatencyMinMS = 2_000 // spec §6.1: slow-carrier bounded 2–4 s
	slowCarrierLatencyMaxMS = 4_000

	pduBufferSizeMin = 1
	portMin          = 1
	portMax          = 65_535
	octetMax         = 255 // addr_ton / addr_npi are single octets
)

// profileSpec captures what config validation needs to know about one profile: the
// set of params knobs it exposes, and — for profiles whose latency is range-bound —
// the latency window. A knob absent from exposes set on this profile is rejected
// (ErrParamNotExposed); latency outside [latencyMinMS, latencyMaxMS], when set, is
// rejected (ErrParamOutOfBounds).
type profileSpec struct {
	exposes      map[string]struct{}
	latencyMinMS uint64
	latencyMaxMS uint64
}

// exposes builds a set from knob names.
func exposes(knobs ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(knobs))
	for _, k := range knobs {
		set[k] = struct{}{}
	}
	return set
}

// profileCatalogue is the frozen validation table. Every profile exposes latency
// implicitly (LatencyConfig is its own block, not a params knob), so exposes lists
// only the scenario.params knobs.
var profileCatalogue = map[Profile]profileSpec{
	ProfileHealthy: {
		exposes:      exposes(),
		latencyMinMS: 0,
		latencyMaxMS: latencyMSMax,
	},
	ProfileFlakyCarrier: {
		exposes:      exposes(knobSuccessRate, knobErrorMix, knobDisconnectIntervalTicks),
		latencyMinMS: 0,
		latencyMaxMS: latencyMSMax,
	},
	ProfileThrottlingCarrier: {
		exposes:      exposes(knobThroughputCapPerSec, knobErrorCode),
		latencyMinMS: 0,
		latencyMaxMS: latencyMSMax,
	},
	ProfileDeadCarrier: {
		exposes:      exposes(knobMode),
		latencyMinMS: 0,
		latencyMaxMS: latencyMSMax,
	},
	ProfileSlowCarrier: {
		exposes:      exposes(),
		latencyMinMS: slowCarrierLatencyMinMS,
		latencyMaxMS: slowCarrierLatencyMaxMS,
	},
	ProfileThroughputCapped: {
		exposes:      exposes(knobThroughputCapPerSec),
		latencyMinMS: 0,
		latencyMaxMS: latencyMSMax,
	},
}
