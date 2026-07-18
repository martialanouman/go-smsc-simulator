package fault

import (
	"math/rand/v2"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

func ptr(v uint64) *uint64 { return &v }

// twinRNGs returns two sources at the identical starting position, so a test can call
// LatencyMS on one and compare the other's next raw draw to detect whether a draw was
// consumed.
func twinRNGs() (a, b *rand.Rand) {
	return rand.New(rand.NewPCG(1, 2)), rand.New(rand.NewPCG(1, 2))
}

func TestLatencyMS_Fixed(t *testing.T) {
	t.Parallel()

	cfg := config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: ptr(40)}}
	used, untouched := twinRNGs()

	if got := LatencyMS(cfg, 1, used); got != 40 {
		t.Fatalf("fixed latency = %d, want 40", got)
	}
	if used.Uint64() != untouched.Uint64() {
		t.Fatalf("fixed distribution must consume no rng draw")
	}
}

func TestLatencyMS_Uniform_InBounds(t *testing.T) {
	t.Parallel()

	cfg := config.LatencyConfig{Distribution: config.LatencyUniform, Params: config.LatencyParams{MinMS: ptr(2000), MaxMS: ptr(4000)}}
	r := rand.New(rand.NewPCG(7, 7))
	for i := 0; i < 10_000; i++ {
		got := LatencyMS(cfg, uint64(i), r)
		if got < 2000 || got > 4000 {
			t.Fatalf("uniform sample %d out of [2000,4000]", got)
		}
	}
}

func TestLatencyMS_Uniform_DegenerateRange(t *testing.T) {
	t.Parallel()

	cfg := config.LatencyConfig{Distribution: config.LatencyUniform, Params: config.LatencyParams{MinMS: ptr(3000), MaxMS: ptr(3000)}}
	used, untouched := twinRNGs()

	if got := LatencyMS(cfg, 1, used); got != 3000 {
		t.Fatalf("degenerate uniform = %d, want 3000", got)
	}
	if used.Uint64() != untouched.Uint64() {
		t.Fatalf("degenerate uniform range must consume no rng draw")
	}
}

func TestLatencyMS_Normal_NeverNegative(t *testing.T) {
	t.Parallel()

	// mean small, stddev large so the truncation at zero is exercised often.
	cfg := config.LatencyConfig{Distribution: config.LatencyNormal, Params: config.LatencyParams{MeanMS: ptr(50), StddevMS: ptr(100)}}
	r := rand.New(rand.NewPCG(3, 9))
	for i := 0; i < 10_000; i++ {
		// uint64 is unsigned; the real assertion is that the internal float truncation
		// at zero holds — verified by the function not panicking and staying bounded.
		if got := LatencyMS(cfg, uint64(i), r); got > 100_000 {
			t.Fatalf("normal sample %d implausibly large — truncation/rounding broken", got)
		}
	}
}

func TestLatencyMS_Spike_TickAnchored(t *testing.T) {
	t.Parallel()

	cfg := config.LatencyConfig{Distribution: config.LatencySpike, Params: config.LatencyParams{BaseMS: ptr(30), SpikeMS: ptr(250), IntervalTicks: ptr(1000)}}
	used, untouched := twinRNGs()

	if got := LatencyMS(cfg, 999, used); got != 30 {
		t.Errorf("non-spike tick latency = %d, want base 30", got)
	}
	if got := LatencyMS(cfg, 1000, used); got != 250 {
		t.Errorf("spike tick latency = %d, want spike 250", got)
	}
	if got := LatencyMS(cfg, 2000, used); got != 250 {
		t.Errorf("spike tick latency = %d, want spike 250", got)
	}
	if used.Uint64() != untouched.Uint64() {
		t.Fatalf("spike distribution must consume no rng draw")
	}
}
