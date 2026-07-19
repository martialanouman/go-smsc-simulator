package smsc

import (
	"log/slog"

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
// all -> always; oldest -> only the oldest bind then open; random -> a per-bind draw from
// the bind's schedule PRNG. when decides the moment relative to the triggering submit's
// response (before_response is handled ahead of the response in handleSubmit).
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
}

// isDisconnectTarget reports whether this bind should cut itself for a disconnect of the
// given scope, evaluated at the moment its clock reaches the disconnect tick.
func (s *session) isDisconnectTarget(scope config.DisconnectScope) bool {
	switch scope {
	case config.DisconnectScopeAll:
		return true
	case config.DisconnectScopeOldest:
		return s.smsc.binds.isOldest(s.id)
	case config.DisconnectScopeRandom:
		return s.scenarioState.DisconnectDraw(s.perBindClock)
	default:
		return false
	}
}

// dueDisconnectBeforeResponse reports whether a scheduled disconnect due at the current
// clock targets this bind AND fires before_response — so handleSubmit can withhold the
// triggering submit's response and cut, matching an OutcomeDisconnect before_response. It
// peeks (DuePending) rather than draining: the event still drains/flushes normally, and a
// scope: random draw is taken at most once (here on the before path, or in dispatch on the
// after/quiescence path — never both for the same event).
func (s *session) dueDisconnectBeforeResponse() bool {
	for _, ev := range s.sched.DuePending(s.perBindClock) {
		if d, ok := ev.Payload.(disconnectEvent); ok &&
			d.when == config.DisconnectBeforeResponse && s.isDisconnectTarget(d.scope) {
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
		if s.isDisconnectTarget(p.scope) {
			s.state = stateClosed
		}
	default:
		// No other payload type is scheduled in this build; ignore defensively.
	}
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
