package smsc

import (
	"cmp"
	"log/slog"
	"slices"

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

	// Transitions and disconnects are NOT put on the Runner. The Runner exists to drain
	// pending OUTPUT (DLR/MO), and its quiescence flush releases everything at once when a
	// bind falls silent. Flushing a transition early would change how later submits are
	// evaluated based on wall-clock silence; flushing a disconnect would cut an idle bind (a
	// receiver never advances its clock) ~quiescence after every bind, whatever at_tick —
	// both break invariant (a). So they live in per-bind cursors, advanced purely by the
	// logical clock at submit time; a bind left idle simply never crosses them. Sorted
	// stably, so same-tick entries keep config order (the last transition at a tick wins).
	s.transitions = sortedByTick(s.smsc.cfg.ScheduledTransitions, func(t config.ScheduledTransition) uint64 { return t.AtTick })
	s.disconnects = sortedByTick(s.smsc.cfg.ScheduledDisconnects, func(d config.ScheduledDisconnect) uint64 { return d.AtTick })
}

// sortedByTick returns a stably at_tick-ordered copy, leaving the config untouched.
func sortedByTick[T any](in []T, tick func(T) uint64) []T {
	return slices.SortedStableFunc(slices.Values(in), func(a, b T) int { return cmp.Compare(tick(a), tick(b)) })
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
// at dueTick. The random coin is keyed to dueTick (the event's at_tick), so the decision is a
// stable property of (bind, at_tick), idempotent across the before/after-response checks.
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
// only peeks; the scope: random coin is idempotent, so applyDueDisconnects agrees with it.
func (s *session) dueDisconnectBeforeResponse() bool {
	for _, d := range s.disconnects[s.disconnectCursor:] {
		if d.AtTick > s.perBindClock {
			break
		}
		if d.When == config.DisconnectBeforeResponse && s.isDisconnectTarget(d.Scope, d.AtTick) {
			return true
		}
	}
	return false
}

// applyDueDisconnects consumes every scheduled disconnect the clock has reached and cuts
// this bind if one targets it. Called after the submit's response (a before_response cut
// already happened in dueDisconnectBeforeResponse).
func (s *session) applyDueDisconnects(tick uint64) {
	for s.disconnectCursor < len(s.disconnects) && s.disconnects[s.disconnectCursor].AtTick <= tick {
		d := s.disconnects[s.disconnectCursor]
		s.disconnectCursor++
		if s.isDisconnectTarget(d.Scope, d.AtTick) {
			s.state = stateClosed
		}
	}
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
	default:
		// No other payload type is scheduled in this build; ignore defensively.
	}
}

// applyTransition swaps this bind's active engine to the target profile and records it on
// the SMSC's read-only observable. buildEngines guarantees an engine exists for every
// transition target, so the lookup cannot miss; a defensive miss is ignored rather than
// panicking a live session.
//
// Known limitation: the throughput gate is bound once at bind time from the initial profile
// and is NOT rebuilt here, so a transition cannot add or remove throttling. Validation
// rejects seeded transitions INTO a throughput profile; the reverse (a throughput initial
// transitioning out) keeps the original gate — acceptable since throughput binds are the
// wall-clock, replay-exempt path (spec §6.2/§6.3).
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
