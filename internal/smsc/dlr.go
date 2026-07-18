package smsc

import (
	"log/slog"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
	"github.com/martialanouman/go-smsc-simulator/internal/schedule"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// dlrEvent is the payload the Schedule Runner carries for a pending delivery receipt:
// the origin correlation id, the resolved outcome, the two ticks that seed the receipt's
// (deterministic) dates, and the origin addresses (swapped when the receipt is built, so
// it flows back to the submitter).
type dlrEvent struct {
	messageID  string
	outcome    scenario.DLROutcome
	originTick uint64 // origin submit's per_bind_clock → "submit date"
	dueTick    uint64 // tick the receipt fires at → "done date"

	sourceAddr    string
	sourceAddrTON uint8
	sourceAddrNPI uint8
	destAddr      string
	destAddrTON   uint8
	destAddrNPI   uint8
	text          string // origin short_message, truncated to the receipt "text:" field
}

// scheduleDLR queues the delivery receipt for a successful submit, due at the origin
// tick plus the profile's configured delay. A receipt can only travel on a bind able to
// receive deliver_sm; since the DLR is anchored to the origin bind (spec §6.3), a pure
// transmitter origin has no return path — that receipt is counted and logged, never
// emitted silently on a bad mapping.
func (s *session) scheduleDLR(messageID string, msg *smpp.Message, plan *scenario.DLRPlan) {
	if !s.canReceive {
		s.smsc.dlrDropped.Add(1)
		s.logger.Warn("dropping DLR: origin bind cannot receive deliver_sm",
			slog.String("message_id", messageID), slog.String("bind_type", s.bindType))
		return
	}
	dueTick := s.perBindClock + plan.DelayTicks
	s.sched.Schedule(dueTick, dlrEvent{
		messageID:     messageID,
		outcome:       plan.Outcome,
		originTick:    s.perBindClock,
		dueTick:       dueTick,
		sourceAddr:    msg.SourceAddr,
		sourceAddrTON: msg.SourceAddrTON,
		sourceAddrNPI: msg.SourceAddrNPI,
		destAddr:      msg.DestAddr,
		destAddrTON:   msg.DestAddrTON,
		destAddrNPI:   msg.DestAddrNPI,
		text:          string(msg.ShortMessage),
	})
}

// drainDue emits every DLR whose due tick the clock has reached (voie a: normal drain on
// an advancing per_bind_clock).
func (s *session) drainDue(clock uint64) {
	for _, ev := range s.sched.DrainDue(clock) {
		s.emitDLR(ev)
	}
}

// flushSchedule emits every pending DLR regardless of tick (voie b: the quiescence
// flush, so a schedule left at rest is never frozen — invariant d).
func (s *session) flushSchedule() {
	for _, ev := range s.sched.DrainAll() {
		s.emitDLR(ev)
	}
}

// emitDLR builds and sends the deliver_sm delivery receipt for one scheduled event. It
// runs on the read goroutine (the sole caller of send), so it never races the outbound
// teardown.
func (s *session) emitDLR(ev schedule.Event) {
	d, ok := ev.Payload.(dlrEvent)
	if !ok {
		return // only DLR events are scheduled at S4; defensive against a future payload
	}
	state, errCode := dlrWireState(d.outcome)
	s.send(smpp.NewDeliveryReceipt(smpp.DeliveryReceipt{
		MessageID:  d.messageID,
		State:      state,
		ErrorCode:  errCode,
		SubmitDate: dlrDate(d.originTick),
		DoneDate:   dlrDate(d.dueTick),
		Text:       d.text,
		// The receipt flows back to the submitter: its source is the origin's dest, its
		// dest the origin's source.
		SourceAddr:    d.destAddr,
		SourceAddrTON: d.destAddrTON,
		SourceAddrNPI: d.destAddrNPI,
		DestAddr:      d.sourceAddr,
		DestAddrTON:   d.sourceAddrTON,
		DestAddrNPI:   d.sourceAddrNPI,
	}))
}

// dlrWireState maps a scenario DLR outcome onto the SMPP message state and the receipt
// "err:" code. Delivered carries err 000; the failure states carry a non-zero code.
func dlrWireState(o scenario.DLROutcome) (smpp.MessageState, string) {
	switch o {
	case scenario.DLRDelivered:
		return smpp.StateDelivered, "000"
	case scenario.DLRExpired:
		return smpp.StateExpired, "001"
	default: // scenario.DLRFailed
		return smpp.StateUndeliverable, "001"
	}
}

// dlrEpoch is the fixed base date for deterministic receipt timestamps. The dates are
// cosmetic (the ESME correlates on message_id, not the date), so anchoring them to a
// per_bind_clock tick offset from a fixed epoch keeps the receipt reproducible
// byte-for-byte in seeded mode without ever reading the wall clock.
var dlrEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// dlrDateSpanMinutes wraps the per-tick date offset at ~100 years, so the tick→duration
// conversion stays well within int64 (the dates are cosmetic and only need to be
// deterministic and roughly monotone, not a real calendar).
const dlrDateSpanMinutes = 100 * 365 * 24 * 60

// dlrDate renders a per_bind_clock tick as the SMPP YYMMDDhhmm receipt-date string,
// offset one minute per tick from dlrEpoch — deterministic and monotone with the tick.
func dlrDate(tick uint64) string {
	mins := tick % dlrDateSpanMinutes // bounded, so the Duration conversion cannot overflow
	return dlrEpoch.Add(time.Duration(mins) * time.Minute).Format("0601021504")
}
