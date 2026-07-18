// Package rng derives the pseudo-random source each bind session draws from. The
// whole determinism story (invariant a) rests here: with a seed set, a bind's stream
// is a pure function of (seed, virtual SMSC name, bind ordinal), so replaying the
// same fixture replays the same draws — never the global PRNG, never a source shared
// between binds, never seeded from the wall clock (CLAUDE.md rule of gold).
package rng

import (
	"hash/fnv"
	"math/rand/v2"
)

// splitmix64 constants: the increment is the golden-ratio odd constant, the two
// multipliers are the standard splitmix64 finalisers. Named so the mixing reads as
// intent rather than magic numbers.
const (
	goldenGamma = 0x9E3779B97F4A7C15
	mix1        = 0xBF58476D1CE4E5B9
	mix2        = 0x94D049BB133111EB
)

// NewBind returns the deterministic PRNG for one bind session. The same tuple always
// yields the same stream, and two binds (different ordinal, name or seed) get
// decorrelated streams — determinism is scoped per bind, not global. This path never
// reads the wall clock.
func NewBind(seed uint64, smscID string, bindOrdinal uint64) *rand.Rand {
	lo, hi := deriveSeed(seed, smscID, bindOrdinal)
	return rand.New(rand.NewPCG(lo, hi))
}

// NewChaos returns a non-deterministic PRNG for unseeded (chaos) mode — the only
// place a random source may be seeded from process entropy. rand/v2's top-level
// source is process-seeded, so two chaos draws give two independent PCG streams; we
// hand back a *rand.Rand so callers keep one uniform type regardless of mode.
func NewChaos() *rand.Rand {
	return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
}

// deriveSeed folds (seed, smscID, bindOrdinal) into the two 64-bit words PCG needs.
// smscID is hashed with FNV-1a — a fixed, non-randomised hash, unlike hash/maphash
// whose per-process key would break replay across runs. splitmix64 finalisation then
// decorrelates adjacent seeds and ordinals so bind N and bind N+1 do not produce
// shifted-but-correlated streams.
func deriveSeed(seed uint64, smscID string, bindOrdinal uint64) (lo, hi uint64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(smscID)) // hash.Hash Write never returns an error
	nameHash := h.Sum64()

	lo = splitmix64(seed ^ nameHash)
	hi = splitmix64(lo + goldenGamma*bindOrdinal + 1)
	return lo, hi
}

// splitmix64 is the standard splitmix64 finaliser: it scrambles one 64-bit word so
// that inputs differing by a single bit map to uncorrelated outputs.
func splitmix64(x uint64) uint64 {
	x += goldenGamma
	x = (x ^ (x >> 30)) * mix1
	x = (x ^ (x >> 27)) * mix2
	return x ^ (x >> 31)
}
