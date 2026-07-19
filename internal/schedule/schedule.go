// Package schedule is the per-bind Schedule Runner: an ordered set of events due at a
// future logical tick (per_bind_clock), drained either when incoming traffic advances
// the clock to the due tick or, when traffic ceases, by a quiescence flush.
//
// It is a pure data structure — no wall clock, no goroutine, no lock — owned by the
// single read goroutine of a session (same ownership rule as the SMPP session state).
// This is a deliberate split (spec §6.3, plan §8): the Runner decides only WHAT and IN
// WHICH ORDER to drain — strictly deterministic in ticks, ordered (DueTick, Seq) — while
// WHEN to drain (which may depend on the wall clock during quiescence) is the caller's
// concern. S4 carries DLR events; S5 reuses the same Runner for MO, scheduled
// disconnects and scenario transitions.
package schedule

import "sort"

// Event is one scheduled action due at DueTick. Seq is a monotonic per-Runner insertion
// counter that breaks ties between events sharing a due tick, making the drain order a
// total, deterministic order — (DueTick, Seq) — independent of any wall-clock timing.
// Payload is opaque: the Runner never inspects it, so it can hold a DLR at S4 and an MO
// or disconnect at S5 without the package knowing anything about them.
type Event struct {
	DueTick uint64
	Seq     uint64
	Payload any
}

// Runner holds one bind's pending schedule, sorted by (DueTick, Seq). It is NOT safe
// for concurrent use: it is owned by a session's read goroutine (CLAUDE.md: "state owned
// by one goroutine, no locks").
type Runner struct {
	pending []Event
	seq     uint64
}

// Schedule adds an event due at dueTick, keeping pending sorted by (DueTick, Seq). The
// per-Runner Seq is assigned here so two events due at the same tick drain in the order
// they were scheduled; it keeps rising across flushes so ordering stays total for the
// whole life of the bind.
func (r *Runner) Schedule(dueTick uint64, payload any) {
	e := Event{DueTick: dueTick, Seq: r.seq, Payload: payload}
	r.seq++
	// Position is the first pending event that sorts after e. Fixed-delay DLRs arrive in
	// non-decreasing dueTick order (usually an append), but the binary search keeps the
	// invariant total for any future out-of-order event (e.g. a uniform-delay DLR at S5+).
	i := sort.Search(len(r.pending), func(i int) bool {
		p := r.pending[i]
		return p.DueTick > e.DueTick || (p.DueTick == e.DueTick && p.Seq > e.Seq)
	})
	r.pending = append(r.pending, Event{})
	copy(r.pending[i+1:], r.pending[i:])
	r.pending[i] = e
}

// DrainDue removes and returns every pending event whose due tick has been reached
// (DueTick <= clock), in (DueTick, Seq) order. This is the normal drain: an advancing
// per_bind_clock releases the events that have come due. Returns nil when none are due.
func (r *Runner) DrainDue(clock uint64) []Event {
	// pending is sorted, so the due events form a prefix ending at the first later tick.
	n := sort.Search(len(r.pending), func(i int) bool { return r.pending[i].DueTick > clock })
	if n == 0 {
		return nil
	}
	due := make([]Event, n)
	copy(due, r.pending[:n])
	r.pending = append(r.pending[:0], r.pending[n:]...)
	return due
}

// DuePending returns, WITHOUT removing them, the events due at or before clock, in the
// same (DueTick, Seq) order DrainDue would yield. It lets the session inspect an imminent
// event — a before_response scheduled disconnect, which must be acted on before the
// current PDU's response is written — while the event itself still drains, and still
// flushes at quiescence, through the normal paths. Returns nil when none are due.
func (r *Runner) DuePending(clock uint64) []Event {
	n := sort.Search(len(r.pending), func(i int) bool { return r.pending[i].DueTick > clock })
	if n == 0 {
		return nil
	}
	out := make([]Event, n)
	copy(out, r.pending[:n])
	return out
}

// DrainAll removes and returns every pending event in (DueTick, Seq) order, regardless
// of tick. This is the quiescence flush: when traffic has ceased the clock will never
// advance again, so the whole schedule is released rather than left frozen (invariant
// d). Returns nil when nothing is pending.
func (r *Runner) DrainAll() []Event {
	if len(r.pending) == 0 {
		return nil
	}
	all := r.pending
	r.pending = nil
	return all
}

// Len reports how many events are still pending — the caller uses it to decide whether a
// quiescence deadline is worth arming at all.
func (r *Runner) Len() int { return len(r.pending) }

// NextDue reports the earliest pending due tick, if any. Present for callers that want
// to arm on the next tick rather than the whole set.
func (r *Runner) NextDue() (uint64, bool) {
	if len(r.pending) == 0 {
		return 0, false
	}
	return r.pending[0].DueTick, true
}
