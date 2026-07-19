package smsc

import (
	"log/slog"
	"sort"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/schedule"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// moEvent is the Schedule Runner payload for a tick-anchored mobile-originated message
// (mo_injection mode: scheduled). Unlike a DLR — planned per submit at a RELATIVE offset
// from the origin tick — the S5 forms (MO, disconnects, transitions) are declared in the
// config at ABSOLUTE at_ticks and enqueued once per bind at bind time. Each bind crosses
// at_tick on its own per_bind_clock, so the whole schedule stays per-bind deterministic
// (invariant a); nothing here reads the wall clock.
type moEvent struct {
	sourceAddr string
	destAddr   string
	content    string
}

// disconnectEvent is the Schedule Runner payload for a scheduled_disconnects[] entry. A
// bind can only ever close ITSELF (the session state is owned by one goroutine, no locks),
// so scope is evaluated as "should I cut myself?" when this bind's clock reaches the tick:
// all -> always; oldest -> only the oldest bind then open; random -> an idempotent per-bind
// coin keyed to (schedule base, tick). when decides the moment relative to the triggering
// submit's response (before_response is handled ahead of the response in handleSubmit).
type disconnectEvent struct {
	scope config.DisconnectScope
	when  config.DisconnectWhen
}

// scheduleConfiguredEvents enqueues, on this bind's own Runner, the tick-anchored events
// the config declares. Called once on a successful bind, so every bind gets its own copy of
// the schedule keyed to its own clock.
//
// auto MO injection is validated at load but not emitted here: anchoring a per-second rate
// to a logical tick counter (a pure RX bind never advances its clock at all) is deferred to
// a later milestone. Only the scheduled mode is wired.
func (s *session) scheduleConfiguredEvents() {
	if mo := s.smsc.cfg.MOInjection; mo != nil && mo.Mode == config.MOModeScheduled {
		for _, ev := range mo.Events {
			s.sched.Schedule(ev.AtTick, moEvent{
				sourceAddr: ev.SourceAddr,
				destAddr:   ev.DestAddr,
				content:    ev.Content,
			})
		}
	}
	for _, d := range s.smsc.cfg.ScheduledDisconnects {
		s.sched.Schedule(d.AtTick, disconnectEvent{scope: d.Scope, when: d.When})
	}

	// Scenario transitions are NOT put on the Runner. The Runner exists to drain pending
	// OUTPUT (DLR/MO/disconnect) — and its quiescence flush releases everything at once when
	// a bind falls silent. A transition emits nothing; it only changes how LATER submits are
	// evaluated. Flushing it early would apply it before the clock truly reached at_tick,
	// altering subsequent outcomes based on wall-clock silence and breaking invariant (a).
	// So transitions live in a per-bind cursor, advanced purely by the logical clock at
	// submit time (applyDueTransitions); a bind left idle simply never crosses them.
	s.transitions = sortedTransitions(s.smsc.cfg.ScheduledTransitions)
}

// sortedTransitions returns the transitions ordered by at_tick, stable so same-tick entries
// keep config order (the last one at a tick wins). It copies, leaving the config untouched.
func sortedTransitions(in []config.ScheduledTransition) []config.ScheduledTransition {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.ScheduledTransition, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].AtTick < out[j].AtTick })
	return out
}

// applyDueTransitions applies every scheduled transition whose at_tick the clock has now
// reached, BEFORE the submit at that tick is evaluated — so the submit AT at_tick already
// runs under the new profile (spec §6.1: "healthy 0-199 -> dead-carrier 200-399" makes tick
// 200 itself dead-carrier). The cursor only moves forward, keyed to the monotonic clock, so
// the whole sequence is a pure function of (transitions, tick): fully reproducible.
func (s *session) applyDueTransitions(tick uint64) {
	for s.transitionCursor < len(s.transitions) && s.transitions[s.transitionCursor].AtTick <= tick {
		s.applyTransition(s.transitions[s.transitionCursor].ToProfile)
		s.transitionCursor++
	}
}

// isDisconnectTarget reports whether this bind should cut itself for a disconnect scheduled
// at dueTick. The random coin is keyed to dueTick (the event's at_tick), NOT the live
// per_bind_clock: a bind flushed at quiescence has a clock short of at_tick, so keying on
// the live clock would make the same scheduled disconnect decide differently depending on how
// far traffic advanced. dueTick keeps the decision a stable property of (bind, at_tick).
func (s *session) isDisconnectTarget(scope config.DisconnectScope, dueTick uint64) bool {
	switch scope {
	case config.DisconnectScopeAll:
		return true
	case config.DisconnectScopeOldest:
		return s.smsc.binds.isOldest(s.id)
	case config.DisconnectScopeRandom:
		return s.scenarioState.DisconnectDraw(dueTick)
	default:
		return false
	}
}

// dueDisconnectBeforeResponse reports whether a scheduled disconnect due at the current
// clock targets this bind AND fires before_response — so handleSubmit can withhold the
// triggering submit's response and cut, matching an OutcomeDisconnect before_response. It
// peeks (DuePending) rather than draining: the event still drains/flushes normally, and the
// scope: random coin is idempotent, so evaluating it here on the before path and again in
// dispatch on the after/quiescence path always agrees.
func (s *session) dueDisconnectBeforeResponse() bool {
	for _, ev := range s.sched.DuePending(s.perBindClock) {
		if d, ok := ev.Payload.(disconnectEvent); ok &&
			d.when == config.DisconnectBeforeResponse && s.isDisconnectTarget(d.scope, ev.DueTick) {
			return true
		}
	}
	return false
}

// dispatch emits or applies one drained schedule event. It runs on the read goroutine (the
// sole caller of send and the sole owner of session state), so it never races the outbound
// teardown. Both drain paths — voie a (drainDue on an advancing clock) and voie b
// (flushSchedule at quiescence) — funnel through here, so a new scheduled mechanism is a
// new payload type plus a case, nothing more. The default is a defensive no-op.
func (s *session) dispatch(ev schedule.Event) {
	switch p := ev.Payload.(type) {
	case dlrEvent:
		s.emitDLR(p)
	case moEvent:
		s.emitMO(p)
	case disconnectEvent:
		// A targeted disconnect cuts this bind. A before_response one that targets this bind
		// on an active submit is handled earlier (peeked, cut before the response), so it
		// never reaches here on voie a; reaching here means either an after_response cut, or
		// a quiescence flush of an idle bind where before/after is moot — close in both.
		if s.isDisconnectTarget(p.scope, ev.DueTick) {
			s.state = stateClosed
		}
	default:
		// No other payload type is scheduled in this build; ignore defensively.
	}
}

// applyTransition swaps this bind's active engine to the target profile and records it on
// the SMSC's read-only observable. buildEngines guarantees an engine exists for every
// transition target, so the lookup cannot miss; a defensive miss is ignored rather than
// panicking a live session.
func (s *session) applyTransition(to config.Profile) {
	engine, ok := s.smsc.engines[to]
	if !ok {
		s.logger.Warn("scheduled transition to unbuilt profile ignored", slog.String("to_profile", string(to)))
		return
	}
	s.currentEngine = engine
	s.smsc.setActiveProfile(to)
}

// emitMO sends a scheduled mobile-originated deliver_sm. Like a DLR it can only travel on a
// bind able to receive deliver_sm (RX/TRX); a transmitter-only bind has no downlink path,
// so the MO is dropped and logged rather than emitted on a bad mapping.
func (s *session) emitMO(m moEvent) {
	if !s.canReceive {
		s.logger.Warn("dropping scheduled MO: bind cannot receive deliver_sm",
			slog.String("bind_type", s.bindType))
		return
	}
	s.send(smpp.NewMobileOriginated(m.sourceAddr, m.destAddr, m.content))
}
