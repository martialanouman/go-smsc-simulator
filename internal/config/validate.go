package config

import (
	"errors"
	"fmt"
	"regexp"
)

// This file is the fail-fast gate. validate runs inside Load, before Load returns,
// so run() can never reach its boot gate with an invalid topology (invariant b): a
// rejected config never leaves a half-open listener behind.
//
// Errors are AGGREGATED via errors.Join: an operator editing a fixture sees every
// fault at once. Checks that depend on an earlier one are GUARDED (an unknown
// profile skips knob checks, an invalid distribution skips latency-param checks) so
// one root fault does not spray misleading cascades. errors.Is still matches each
// sentinel through the join, and each message names the offending .yml field.

// Acceptance sentinels — one per S1 rule (plan §5).
var (
	// ErrUnknownProfile flags a scenario.profile outside the frozen catalogue.
	ErrUnknownProfile = errors.New("unknown scenario profile")
	// ErrSeededWallclock flags clock: wallclock combined with a seed (chaos-only).
	ErrSeededWallclock = errors.New("wallclock clock requires no seed")
	// ErrDuplicatePort flags two virtual SMSCs sharing a listen port.
	ErrDuplicatePort = errors.New("duplicate virtual smsc port")
	// ErrParamOutOfBounds flags a numeric parameter outside its allowed range.
	ErrParamOutOfBounds = errors.New("scenario parameter out of bounds")
	// ErrUnknownToProfile flags a scheduled transition targeting an unknown profile.
	ErrUnknownToProfile = errors.New("unknown scheduled transition profile")
)

// Supporting sentinels — schema coherence beyond the five headline rules.
var (
	// ErrParamNotExposed flags a knob set that the active profile (or distribution)
	// does not expose.
	ErrParamNotExposed = errors.New("parameter not exposed")
	// ErrMissingParam flags a required knob left absent.
	ErrMissingParam = errors.New("required parameter absent")
	// ErrInvalidEnum flags an out-of-range enumerated value.
	ErrInvalidEnum = errors.New("invalid enumerated value")
	// ErrInvalidAddressRange flags an address_range that does not compile as a regexp.
	ErrInvalidAddressRange = errors.New("invalid address_range regexp")
)

// Latency params knob names, matching LatencyParams yaml keys.
const (
	latMS            = "ms"
	latMinMS         = "min_ms"
	latMaxMS         = "max_ms"
	latMeanMS        = "mean_ms"
	latStddevMS      = "stddev_ms"
	latBaseMS        = "base_ms"
	latSpikeMS       = "spike_ms"
	latIntervalTicks = "interval_ticks"
)

// validate reports every way cfg violates the §3.1 contract, joined into one error.
//
// fail-fast: rejected at config load.
func validate(cfg *Config) error {
	var errs []error

	if cfg.Observability != nil {
		if p := cfg.Observability.HTTPPort; p < 0 || p > portMax {
			errs = append(errs, fmt.Errorf("%w: observability.http_port %d not in [0,%d]",
				ErrParamOutOfBounds, p, portMax))
		}
	}

	// Seed the port map with the observability port so an SMPP listener colliding
	// with the HTTP surface is rejected at load, not at bind. Port 0 (OS-assigned
	// ephemeral) never collides, so it is not registered.
	seenPort := make(map[int]string, len(cfg.VirtualSMSCs)+1)
	if cfg.Observability != nil && cfg.Observability.HTTPPort > 0 {
		seenPort[cfg.Observability.HTTPPort] = "observability.http_port"
	}
	for i := range cfg.VirtualSMSCs {
		vs := &cfg.VirtualSMSCs[i]
		errs = append(errs, validateVirtualSMSC(vs)...)

		if prev, dup := seenPort[vs.Port]; dup {
			errs = append(errs, fmt.Errorf("%w: port %d shared by %q and %q",
				ErrDuplicatePort, vs.Port, prev, vs.Name))
		} else {
			seenPort[vs.Port] = vs.Name
		}
	}

	// Join(nil...) == nil, so a clean config returns nil.
	return errors.Join(errs...)
}

// validateVirtualSMSC checks one virtual SMSC entry. It returns every fault so the
// caller can aggregate across the whole topology.
func validateVirtualSMSC(vs *VirtualSMSCConfig) []error {
	errs := make([]error, 0, len(vs.ScheduledDisconnects)+len(vs.ScheduledTransitions))
	seeded := vs.Seed != nil

	if vs.Name == "" {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[].name is empty", ErrMissingParam))
	}
	if vs.Port < portMin || vs.Port > portMax {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].port %d not in [%d,%d]",
			ErrParamOutOfBounds, vs.Name, vs.Port, portMin, portMax))
	}
	if vs.AddrTON < 0 || vs.AddrTON > octetMax {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].addr_ton %d not in [0,%d]",
			ErrParamOutOfBounds, vs.Name, vs.AddrTON, octetMax))
	}
	if vs.AddrNPI < 0 || vs.AddrNPI > octetMax {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].addr_npi %d not in [0,%d]",
			ErrParamOutOfBounds, vs.Name, vs.AddrNPI, octetMax))
	}
	if vs.PDUBufferSize < pduBufferSizeMin {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].pdu_buffer_size %d below %d",
			ErrParamOutOfBounds, vs.Name, vs.PDUBufferSize, pduBufferSizeMin))
	}
	if vs.ThroughputLimitPerSec != nil && *vs.ThroughputLimitPerSec < 1 {
		errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].throughput_limit_per_sec %d below 1",
			ErrParamOutOfBounds, vs.Name, *vs.ThroughputLimitPerSec))
	}
	if vs.AddressRange != "" {
		if _, err := regexp.Compile(vs.AddressRange); err != nil {
			// Compiled value is discarded: Config stays pure data at S1. RE2 has no
			// catastrophic backtracking, so there is no ReDoS bound to enforce.
			errs = append(errs, fmt.Errorf("%w: virtual_smscs[%q].address_range %q: %v",
				ErrInvalidAddressRange, vs.Name, vs.AddressRange, err))
		}
	}

	errs = append(errs, validateScenario(&vs.Scenario, seeded)...)
	errs = append(errs, validateMOInjection(vs.MOInjection, seeded)...)

	for i := range vs.ScheduledDisconnects {
		errs = append(errs, validateScheduledDisconnect(&vs.ScheduledDisconnects[i])...)
	}
	for i := range vs.ScheduledTransitions {
		if !vs.ScheduledTransitions[i].ToProfile.Valid() {
			errs = append(errs, fmt.Errorf("%w: scheduled_transitions[%d].to_profile %q",
				ErrUnknownToProfile, i, vs.ScheduledTransitions[i].ToProfile))
		}
	}

	return errs
}

// validateScenario gates on the profile: an unknown profile yields exactly one
// error (there is no known knob set to check against), avoiding a cascade.
func validateScenario(sc *ScenarioConfig, seeded bool) []error {
	spec, known := profileCatalogue[sc.Profile]
	if !known {
		return []error{fmt.Errorf("%w: scenario.profile %q", ErrUnknownProfile, sc.Profile)}
	}

	errs := validateScenarioParams(sc.Profile, sc.Params, spec)
	errs = append(errs, validateLatency(&sc.Latency, spec)...)
	errs = append(errs, validateDLR(sc.DLR, seeded)...)
	return errs
}

// validateScenarioParams enforces that only the knobs the profile exposes are set,
// and that each set-and-exposed knob is in range.
func validateScenarioParams(profile Profile, p ScenarioParams, spec profileSpec) []error {
	var errs []error

	for _, knob := range setParamKnobs(p) {
		if _, ok := spec.exposes[knob]; !ok {
			errs = append(errs, fmt.Errorf("%w: scenario.params.%s not exposed by profile %q",
				ErrParamNotExposed, knob, profile))
		}
	}

	if p.SuccessRate != nil {
		if _, ok := spec.exposes[knobSuccessRate]; ok && (*p.SuccessRate < 0 || *p.SuccessRate > 1) {
			errs = append(errs, fmt.Errorf("%w: scenario.params.success_rate %g not in [0,1]",
				ErrParamOutOfBounds, *p.SuccessRate))
		}
	}
	if p.ThroughputCapPerSec != nil {
		if _, ok := spec.exposes[knobThroughputCapPerSec]; ok &&
			(*p.ThroughputCapPerSec < throughputCapMin || *p.ThroughputCapPerSec > throughputCapMax) {
			errs = append(errs, fmt.Errorf("%w: scenario.params.throughput_cap_per_sec %d not in [%d,%d]",
				ErrParamOutOfBounds, *p.ThroughputCapPerSec, throughputCapMin, throughputCapMax))
		}
	}
	if p.DisconnectIntervalTicks != nil {
		if _, ok := spec.exposes[knobDisconnectIntervalTicks]; ok && *p.DisconnectIntervalTicks < 1 {
			errs = append(errs, fmt.Errorf("%w: scenario.params.disconnect_interval_ticks %d below 1",
				ErrParamOutOfBounds, *p.DisconnectIntervalTicks))
		}
	}
	if p.ErrorCode != nil {
		if _, ok := spec.exposes[knobErrorCode]; ok && !p.ErrorCode.Valid() {
			errs = append(errs, fmt.Errorf("%w: scenario.params.error_code %q",
				ErrInvalidEnum, *p.ErrorCode))
		}
	}
	if p.Mode != nil {
		if _, ok := spec.exposes[knobMode]; ok && !p.Mode.Valid() {
			errs = append(errs, fmt.Errorf("%w: scenario.params.mode %q", ErrInvalidEnum, *p.Mode))
		}
	}
	// error_mix keys bypass KnownFields (map), so each key is validated here.
	if p.ErrorMix != nil {
		if _, ok := spec.exposes[knobErrorMix]; ok {
			var sum uint
			for code, weight := range p.ErrorMix {
				if !code.Valid() {
					errs = append(errs, fmt.Errorf("%w: scenario.params.error_mix key %q",
						ErrInvalidEnum, code))
				}
				sum += weight
			}
			if sum == 0 {
				errs = append(errs, fmt.Errorf("%w: scenario.params.error_mix weights sum to zero",
					ErrParamOutOfBounds))
			}
		}
	}

	return errs
}

// setParamKnobs lists the params knobs actually set on p (non-nil pointer, non-nil
// map). It is what the exposed-knob check reads.
func setParamKnobs(p ScenarioParams) []string {
	var knobs []string
	if p.SuccessRate != nil {
		knobs = append(knobs, knobSuccessRate)
	}
	if p.ErrorMix != nil {
		knobs = append(knobs, knobErrorMix)
	}
	if p.DisconnectIntervalTicks != nil {
		knobs = append(knobs, knobDisconnectIntervalTicks)
	}
	if p.ThroughputCapPerSec != nil {
		knobs = append(knobs, knobThroughputCapPerSec)
	}
	if p.ErrorCode != nil {
		knobs = append(knobs, knobErrorCode)
	}
	if p.Mode != nil {
		knobs = append(knobs, knobMode)
	}
	return knobs
}

// validateLatency guards on a valid distribution, then enforces that exactly the
// distribution's params keys are set and that latency positions fall in the
// profile's latency window.
func validateLatency(lat *LatencyConfig, spec profileSpec) []error {
	if !lat.Distribution.Valid() {
		return []error{fmt.Errorf("%w: scenario.latency.distribution %q",
			ErrInvalidEnum, lat.Distribution)}
	}

	set := setLatencyKnobs(lat.Params)
	required := latencyRequiredKnobs(lat.Distribution)
	allowed := make(map[string]struct{}, len(required))
	for _, k := range required {
		allowed[k] = struct{}{}
	}

	var errs []error
	for knob := range set {
		if _, ok := allowed[knob]; !ok {
			errs = append(errs, fmt.Errorf("%w: scenario.latency.params.%s not used by distribution %q",
				ErrParamNotExposed, knob, lat.Distribution))
		}
	}
	for _, knob := range required {
		if _, ok := set[knob]; !ok {
			errs = append(errs, fmt.Errorf("%w: scenario.latency.params.%s for distribution %q",
				ErrMissingParam, knob, lat.Distribution))
		}
	}

	// Bounds: positional latency values must fall in the profile window. stddev_ms is
	// a spread and interval_ticks is a tick count, so neither is a position bound.
	for _, knob := range []string{latMS, latMinMS, latMaxMS, latMeanMS, latBaseMS, latSpikeMS} {
		if v, ok := set[knob]; ok && (*v < spec.latencyMinMS || *v > spec.latencyMaxMS) {
			errs = append(errs, fmt.Errorf("%w: scenario.latency.params.%s %d not in [%d,%d]",
				ErrParamOutOfBounds, knob, *v, spec.latencyMinMS, spec.latencyMaxMS))
		}
	}
	if lo, ok := set[latMinMS]; ok {
		if hi, ok := set[latMaxMS]; ok && *lo > *hi {
			errs = append(errs, fmt.Errorf("%w: scenario.latency.params.min_ms %d above max_ms %d",
				ErrParamOutOfBounds, *lo, *hi))
		}
	}
	if v, ok := set[latIntervalTicks]; ok && *v < 1 {
		errs = append(errs, fmt.Errorf("%w: scenario.latency.params.interval_ticks %d below 1",
			ErrParamOutOfBounds, *v))
	}

	return errs
}

// setLatencyKnobs maps each set latency knob to its value.
func setLatencyKnobs(p LatencyParams) map[string]*uint64 {
	set := make(map[string]*uint64, 3)
	for knob, v := range map[string]*uint64{
		latMS: p.MS, latMinMS: p.MinMS, latMaxMS: p.MaxMS, latMeanMS: p.MeanMS,
		latStddevMS: p.StddevMS, latBaseMS: p.BaseMS, latSpikeMS: p.SpikeMS,
		latIntervalTicks: p.IntervalTicks,
	} {
		if v != nil {
			set[knob] = v
		}
	}
	return set
}

// latencyRequiredKnobs returns the params keys a distribution needs.
func latencyRequiredKnobs(d LatencyDistribution) []string {
	switch d {
	case LatencyFixed:
		return []string{latMS}
	case LatencyUniform:
		return []string{latMinMS, latMaxMS}
	case LatencyNormal:
		return []string{latMeanMS, latStddevMS}
	case LatencySpike:
		return []string{latBaseMS, latSpikeMS, latIntervalTicks}
	default:
		return nil
	}
}

// validateDLR checks optional DLR generation. A nil block means no DLRs.
func validateDLR(dlr *DLRConfig, seeded bool) []error {
	if dlr == nil {
		return nil
	}

	var errs []error
	if !clockValid(dlr.Clock) {
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.clock %q", ErrInvalidEnum, dlr.Clock))
	} else if seeded && dlr.Clock == ClockWallclock {
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.clock is wallclock but seed is set",
			ErrSeededWallclock))
	}

	// S1 supports only the fixed DLR delay; uniform bounds are reserved for a later
	// milestone (schema.go). A non-fixed distribution has no usable params in
	// DLRDelay, so it is rejected here rather than silently producing a dead delay.
	switch {
	case dlr.Delay.Distribution != LatencyFixed:
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.delay.distribution %q: only fixed is supported",
			ErrInvalidEnum, dlr.Delay.Distribution))
	case dlr.Delay.Ticks == nil:
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.delay.ticks for distribution fixed",
			ErrMissingParam))
	}
	// min_ticks/max_ticks belong to the reserved uniform delay; flag them as unused
	// under fixed, mirroring the exposed-knob discipline elsewhere.
	if dlr.Delay.MinTicks != nil {
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.delay.min_ticks unused by distribution fixed",
			ErrParamNotExposed))
	}
	if dlr.Delay.MaxTicks != nil {
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.delay.max_ticks unused by distribution fixed",
			ErrParamNotExposed))
	}

	if dlr.OutcomeWeights.Delivered+dlr.OutcomeWeights.Failed+dlr.OutcomeWeights.Expired == 0 {
		errs = append(errs, fmt.Errorf("%w: scenario.dlr.outcome_weights sum to zero",
			ErrParamOutOfBounds))
	}

	return errs
}

// validateMOInjection checks optional MO injection. A nil block means no MO.
func validateMOInjection(mo *MOInjectionConfig, seeded bool) []error {
	if mo == nil {
		return nil
	}

	var errs []error
	if !mo.Mode.Valid() {
		// Unknown mode: no per-mode field expectations to check, so stop here.
		return []error{fmt.Errorf("%w: mo_injection.mode %q", ErrInvalidEnum, mo.Mode)}
	}
	if !clockValid(mo.Clock) {
		errs = append(errs, fmt.Errorf("%w: mo_injection.clock %q", ErrInvalidEnum, mo.Clock))
	} else if seeded && mo.Clock == ClockWallclock {
		errs = append(errs, fmt.Errorf("%w: mo_injection.clock is wallclock but seed is set",
			ErrSeededWallclock))
	}

	switch mo.Mode {
	case MOModeScheduled:
		if len(mo.Events) == 0 {
			errs = append(errs, fmt.Errorf("%w: mo_injection.events for mode scheduled",
				ErrMissingParam))
		}
		if mo.RatePerSec != nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.rate_per_sec unused in mode scheduled",
				ErrParamNotExposed))
		}
		if mo.ContentTemplate != nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.content_template unused in mode scheduled",
				ErrParamNotExposed))
		}
	case MOModeAuto:
		if mo.RatePerSec == nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.rate_per_sec for mode auto",
				ErrMissingParam))
		} else if *mo.RatePerSec < 1 {
			errs = append(errs, fmt.Errorf("%w: mo_injection.rate_per_sec %d below 1",
				ErrParamOutOfBounds, *mo.RatePerSec))
		}
		if mo.ContentTemplate == nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.content_template for mode auto",
				ErrMissingParam))
		}
		if len(mo.Events) > 0 {
			errs = append(errs, fmt.Errorf("%w: mo_injection.events unused in mode auto",
				ErrParamNotExposed))
		}
	case MOModeDisabled:
		// A disabled block injects nothing, so any per-mode field set alongside it is
		// dead config — flag it, mirroring the scheduled/auto unused-field checks.
		if mo.RatePerSec != nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.rate_per_sec unused in mode disabled",
				ErrParamNotExposed))
		}
		if mo.ContentTemplate != nil {
			errs = append(errs, fmt.Errorf("%w: mo_injection.content_template unused in mode disabled",
				ErrParamNotExposed))
		}
		if len(mo.Events) > 0 {
			errs = append(errs, fmt.Errorf("%w: mo_injection.events unused in mode disabled",
				ErrParamNotExposed))
		}
	}

	return errs
}

// validateScheduledDisconnect checks a scheduled disconnect's enums.
func validateScheduledDisconnect(d *ScheduledDisconnect) []error {
	var errs []error
	if !d.Scope.Valid() {
		errs = append(errs, fmt.Errorf("%w: scheduled_disconnects[].scope %q", ErrInvalidEnum, d.Scope))
	}
	if !d.When.Valid() {
		errs = append(errs, fmt.Errorf("%w: scheduled_disconnects[].when %q", ErrInvalidEnum, d.When))
	}
	return errs
}

// clockValid treats an omitted clock as the safe default (logical), which is valid
// in both seeded and chaos modes; only an explicit unknown string is rejected.
func clockValid(c Clock) bool {
	return c == "" || c.Valid()
}
