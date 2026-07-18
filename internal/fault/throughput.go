package fault

import "time"

// WallWindow enforces a throughput cap of capPerSec submits per one-second wall-clock
// window. It is the ONLY fault mechanism that reads the wall clock on the decision
// path — deliberately: a throughput rate is a real-time property (spec §6.2/§6.3),
// used to exercise the gateway's adaptive throttling (§6.4). Binds using it are
// therefore NOT part of the byte-for-byte replay corpus (invariant a); they are
// asserted by threshold and statistical aggregation instead.
//
// A WallWindow is owned by a single bind session (readLoop), like the rest of the
// session state, so it is never accessed concurrently and holds no lock. now is a
// seam for tests to drive the clock without sleeping.
type WallWindow struct {
	capPerSec   int
	windowStart time.Time
	count       int
	now         func() time.Time
}

// NewWallWindow builds a throughput gate for the given per-second cap.
func NewWallWindow(capPerSec int) *WallWindow {
	return &WallWindow{capPerSec: capPerSec, now: time.Now}
}

// Allow reports whether the current submit is within the cap for its window. It opens
// a fresh window once a second has elapsed since the last one started; within a
// window the first capPerSec submits are allowed and the rest throttled.
func (g *WallWindow) Allow() bool {
	t := g.now()
	if t.Sub(g.windowStart) >= time.Second {
		g.windowStart = t
		g.count = 0
	}
	g.count++
	return g.count <= g.capPerSec
}
