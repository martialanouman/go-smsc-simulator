package rng

import (
	"math/rand/v2"
	"testing"
)

// draws pulls the first n uint64s from a source so two streams can be compared.
func draws(r *rand.Rand, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = r.Uint64()
	}
	return out
}

func equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewBind_SameTupleSameStream(t *testing.T) {
	t.Parallel()

	first := draws(NewBind(42, "carrier-a", 1), 16)
	second := draws(NewBind(42, "carrier-a", 1), 16)

	if !equal(first, second) {
		t.Fatalf("same (seed, smscID, bindOrdinal) must yield the same stream")
	}
}

func TestNewBind_DecorrelatedByEveryInput(t *testing.T) {
	t.Parallel()

	base := draws(NewBind(42, "carrier-a", 1), 16)

	cases := map[string][]uint64{
		"different seed":    draws(NewBind(43, "carrier-a", 1), 16),
		"different smscID":  draws(NewBind(42, "carrier-b", 1), 16),
		"different ordinal": draws(NewBind(42, "carrier-a", 2), 16),
	}
	for name, other := range cases {
		if equal(base, other) {
			t.Errorf("%s must produce a divergent stream, got identical draws", name)
		}
	}
}

func TestNewBind_AdjacentOrdinalsDiverge(t *testing.T) {
	t.Parallel()

	// Adjacent bind ordinals are the common concurrent case; the splitmix64 finalise
	// must keep them from producing shifted-but-correlated streams.
	prev := draws(NewBind(7, "carrier", 0), 8)
	for ord := uint64(1); ord < 32; ord++ {
		cur := draws(NewBind(7, "carrier", ord), 8)
		if equal(prev, cur) {
			t.Fatalf("ordinal %d and %d produced identical streams", ord-1, ord)
		}
		prev = cur
	}
}

func TestNewChaos_UsableAndIndependent(t *testing.T) {
	t.Parallel()

	// No reproducibility is claimed for chaos mode — only that it yields a usable,
	// non-panicking source and that two instances are independent.
	if equal(draws(NewChaos(), 16), draws(NewChaos(), 16)) {
		t.Fatalf("two chaos sources must be independent")
	}
}
