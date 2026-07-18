// Package fault is the fault injector: it turns a latency config into a served delay
// and enforces the throughput cap. The scenario engine owns outcome selection and
// calls into here for the timing pieces. Latency draws come from the bind's seeded
// PRNG in a fixed order (see LatencyMS), so a seeded replay reproduces every delay;
// the throughput cap is the one real-time, wall-clock mechanism (throughput.go).
package fault

import (
	"math"
	"math/rand/v2"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// LatencyMS returns the served latency in milliseconds for the submit at tick, drawn
// from rng for the random distributions.
//
// Draw discipline (anchored to perBindClock): for a given config the number of rng
// draws is fixed and stable across runs, so a seeded replay stays aligned — uniform
// and normal each draw exactly once; fixed and spike draw nothing (spike is a pure
// function of the tick). Callers must invoke this at the SAME position in their
// per-tick draw order every tick.
func LatencyMS(cfg config.LatencyConfig, tick uint64, rng *rand.Rand) uint64 {
	switch cfg.Distribution {
	case config.LatencyFixed:
		return deref(cfg.Params.MS)

	case config.LatencyUniform:
		lo, hi := deref(cfg.Params.MinMS), deref(cfg.Params.MaxMS)
		if hi <= lo {
			return lo // degenerate range: no draw, both runs agree
		}
		return lo + rng.Uint64N(hi-lo+1)

	case config.LatencyNormal:
		mean, stddev := deref(cfg.Params.MeanMS), deref(cfg.Params.StddevMS)
		v := float64(mean) + float64(stddev)*rng.NormFloat64()
		if v <= 0 {
			return 0 // normal is truncated at zero (spec §6.2)
		}
		return uint64(math.Round(v))

	case config.LatencySpike:
		interval := deref(cfg.Params.IntervalTicks)
		if interval != 0 && tick%interval == 0 { // anchored to perBindClock
			return deref(cfg.Params.SpikeMS)
		}
		return deref(cfg.Params.BaseMS)

	default:
		return 0
	}
}

// deref reads an optional latency param, treating absent as zero. Validation
// guarantees the params the chosen distribution needs are present; this is defensive.
func deref(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
