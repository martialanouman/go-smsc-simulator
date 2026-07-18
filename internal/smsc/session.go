package smsc

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/recorder"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
	"github.com/martialanouman/go-smsc-simulator/internal/schedule"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// sessionState is the SMPP session lifecycle: open → bound → (unbinding) → closed.
// It is owned solely by the session's read goroutine — never shared, never locked.
type sessionState int

const (
	stateOpen sessionState = iota
	stateBound
	stateClosed
)

// maxServedLatencyMS bounds the served-latency wait so the millisecond→Duration
// conversion cannot overflow; no real scenario configures anywhere near a day.
const maxServedLatencyMS = 24 * 60 * 60 * 1000

// serverInitiatedSeq is the sequence number the simulator stamps on a PDU it
// originates rather than answers — currently the shutdown unbind. It sits at the top
// of the valid range to avoid colliding with a client's from-one request sequence.
const serverInitiatedSeq uint32 = 0x7FFFFFFF

// Connection deadlines are robustness bounds, off the deterministic decision path
// (they influence whether a connection lives, never a scenario Decision), so reading
// the wall clock here is legitimate even in seeded mode. writeTimeout stops a client
// that has stopped reading from wedging the writer goroutine; idleTimeout reaps a
// half-open or silent bind (a live client's enquire_link keepalives reset it).
const (
	writeTimeout = 10 * time.Second
	idleTimeout  = 5 * time.Minute
)

// session drives one client connection. The read goroutine (readLoop) owns all
// session state and decodes/handles PDUs; a separate writer goroutine owns the
// socket writes, draining the outbound channel. This split is what lets S4/S5 emit
// asynchronous deliver_sm (DLR/MO) onto outbound while reads continue (plan §6, §8).
type session struct {
	id     uint64
	conn   net.Conn
	smsc   *virtualSMSC
	quit   <-chan struct{}
	logger *slog.Logger

	// owned by readLoop:
	state         sessionState
	bindType      string
	canSubmit     bool
	canReceive    bool // a receiver/transceiver bind may take an async deliver_sm (DLR/MO)
	systemID      string
	perBindClock  uint64
	scenarioState *scenario.BindState // created at successful bind; nil until then

	// sched is this bind's pending tick-scheduled events (DLRs at S4). It is drained by
	// the read goroutine — on a submit that advances the clock (voie a) or, after the
	// quiescence window of no submit_sm, by a flush (voie b) — so all emission stays on
	// readLoop and never races the outbound teardown. lastSubmit anchors the window.
	sched      schedule.Runner
	quiescence time.Duration
	lastSubmit time.Time

	// bound is read by the closer goroutine (a different goroutine than readLoop) to
	// decide whether a graceful shutdown warrants an unbind, so it is atomic.
	bound atomic.Bool

	outbound     chan []byte
	writerClosed chan struct{}
}

func newSession(id uint64, conn net.Conn, v *virtualSMSC, quit <-chan struct{}) *session {
	return &session{
		id:           id,
		conn:         conn,
		smsc:         v,
		quit:         quit,
		logger:       v.logger.With(slog.Uint64("bind_id", id)),
		outbound:     make(chan []byte, 8),
		writerClosed: make(chan struct{}),
		quiescence:   time.Duration(v.cfg.EffectiveQuiescenceFlushMs()) * time.Millisecond,
	}
}

// run owns the whole session lifetime: it starts the writer and a closer that drops
// the connection on engine shutdown, runs the read loop, then tears everything down
// in order so a queued response (e.g. unbind_resp) still reaches the wire before the
// socket closes.
func (s *session) run() {
	done := make(chan struct{})
	closerDone := make(chan struct{})

	// closer: on engine shutdown, unbind bound clients gracefully rather than dropping
	// the TCP connection under them (CLAUDE.md: "unbind propre des binds sur SIGTERM").
	// It queues the unbind, then sets a past read deadline to unblock readLoop; teardown
	// then flushes the queued unbind before closing the socket. Best-effort: it does not
	// wait for unbind_resp.
	go func() {
		defer close(closerDone)
		select {
		case <-s.quit:
			// Unblock the read first, then queue the unbind: if the writer is wedged, the
			// send can block up to writeTimeout, and we must not delay readLoop's exit by
			// that long. Teardown still flushes the queued unbind before closing the socket.
			_ = s.conn.SetReadDeadline(time.Now())
			if s.bound.Load() {
				s.send(&smpp.PDU{CommandID: smpp.Unbind, SequenceNumber: serverInitiatedSeq})
			}
		case <-done:
		}
	}()

	go s.writeLoop()

	s.readLoop()

	// Teardown order matters. First stop the closer and wait until it can no longer
	// call send (a session can end on its own — a disconnect outcome, a client hang-up —
	// while the engine shuts down concurrently; without this the closer could send on a
	// closed outbound and panic). Only then close outbound, flush the writer, close the
	// socket and deregister the bind.
	close(done)
	<-closerDone
	close(s.outbound)
	<-s.writerClosed
	_ = s.conn.Close()
	s.smsc.binds.remove(s.id)
}

// writeLoop is the sole writer of the connection. It drains outbound until the
// channel is closed (clean teardown) or a write fails (broken peer).
func (s *session) writeLoop() {
	defer close(s.writerClosed)
	for b := range s.outbound {
		// A write deadline per write: a client that stopped reading must not wedge this
		// goroutine indefinitely — the deadline turns it into a write error and teardown.
		_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := s.conn.Write(b); err != nil {
			return
		}
	}
}

// send encodes and queues a PDU for the writer. It never blocks the read goroutine
// past the writer's life: if the writer has gone, the PDU is dropped rather than
// deadlocking on a full channel.
func (s *session) send(pdu *smpp.PDU) {
	b, err := smpp.Encode(pdu)
	if err != nil {
		s.logger.Error("encode response", slog.String("command", pdu.CommandID.String()), slog.Any("error", err))
		return
	}
	select {
	case s.outbound <- b:
	case <-s.writerClosed:
	}
}

// armReadDeadline sets the read deadline for the next blocking read. With no events
// pending it is the long idle-reap window; with events pending it is shortened to the
// quiescence window (measured from the last submit) so the flush can fire while the bind
// sits silent — whichever is sooner, so idle-reaping still bounds a truly dead bind.
func (s *session) armReadDeadline() {
	deadline := time.Now().Add(idleTimeout)
	if s.sched.Len() > 0 {
		if q := s.lastSubmit.Add(s.quiescence); q.Before(deadline) {
			deadline = q
		}
	}
	_ = s.conn.SetReadDeadline(deadline)
}

// isTimeout reports whether err is a read-deadline timeout (as opposed to a closed
// connection or a truncation), i.e. a quiescence/idle wakeup rather than a real failure.
func (s *session) isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// readLoop reads, decodes and handles PDUs until the connection ends or a handler
// closes the session. A decode failure is answered with generic_nack rather than
// dropping the link, echoing the sequence number straight from the frame.
func (s *session) readLoop() {
	for {
		// Re-arm the read deadline each iteration: any PDU (including an enquire_link
		// keepalive) resets it. With events pending, the deadline is shortened to the
		// quiescence window so the flush can fire on a silent bind; otherwise it is the
		// long idle-reap window.
		s.armReadDeadline()

		// Check for shutdown AFTER re-arming and immediately before blocking. The closer
		// sets a past read deadline to unblock this read on shutdown; if that happened
		// just before the re-arm above, we have clobbered it with a far-future deadline
		// and would otherwise block until idleTimeout, stalling graceful shutdown (which
		// joins this goroutine via wg.Wait). The conn's internal lock orders the closer's
		// SetReadDeadline before ours in exactly that case, so close(quit) is guaranteed
		// visible here and we exit. If instead our re-arm ran first, the closer's later
		// past deadline unblocks the ReadPDU below normally.
		select {
		case <-s.quit:
			return
		default:
		}

		frame, err := smpp.ReadPDU(s.conn)
		if err != nil {
			// A read-deadline timeout is not necessarily the end of the session: it is how
			// the quiescence flush is driven. Shutdown takes priority; then, while events
			// remain pending, flush once the idle window has elapsed and keep the bind
			// alive; only a timeout with nothing pending reaps a genuinely silent bind.
			if s.isTimeout(err) {
				select {
				case <-s.quit:
					return
				default:
				}
				if s.sched.Len() > 0 {
					if time.Since(s.lastSubmit) >= s.quiescence {
						s.flushSchedule() // voie b: silence past the window drains pending events in tick order
					}
					continue
				}
				return // idle timeout with no pending events: reap the silent bind
			}
			if !errors.Is(err, net.ErrClosed) {
				s.logger.Debug("read loop ended", slog.Any("error", err))
			}
			return
		}

		pdu, err := smpp.Decode(frame)
		if err != nil {
			seq := binary.BigEndian.Uint32(frame[12:16]) // frame is >= 16 bytes (ReadPDU)
			status := smpp.StatusSysErr
			if errors.Is(err, smpp.ErrUnknownCommand) {
				status = smpp.StatusInvCmdID
			}
			s.send(&smpp.PDU{CommandID: smpp.GenericNack, CommandStatus: status, SequenceNumber: seq})
			continue
		}

		s.handle(pdu)
		if s.state == stateClosed {
			return
		}
	}
}

func (s *session) handle(pdu *smpp.PDU) {
	switch pdu.CommandID {
	case smpp.BindTransmitter, smpp.BindReceiver, smpp.BindTransceiver:
		s.handleBind(pdu)
	case smpp.SubmitSM:
		s.handleSubmit(pdu)
	case smpp.EnquireLink:
		s.send(&smpp.PDU{CommandID: smpp.EnquireLinkResp, CommandStatus: smpp.StatusROK, SequenceNumber: pdu.SequenceNumber})
	case smpp.Unbind:
		s.send(&smpp.PDU{CommandID: smpp.UnbindResp, CommandStatus: smpp.StatusROK, SequenceNumber: pdu.SequenceNumber})
		s.state = stateClosed
	default:
		s.send(&smpp.PDU{CommandID: smpp.GenericNack, CommandStatus: smpp.StatusInvCmdID, SequenceNumber: pdu.SequenceNumber})
	}
}

// handleBind authenticates the bind in constant time and, on success, registers the
// session. A wrong credential is answered with ESME_RBINDFAIL and the link is closed,
// as a real SMSC would (this is also the seam dead-carrier's reject_bind reuses at S3).
func (s *session) handleBind(pdu *smpp.PDU) {
	bindType, respID := bindKind(pdu.CommandID)

	if s.state != stateOpen {
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusInvBndSts, SequenceNumber: pdu.SequenceNumber})
		return
	}

	bind, ok := pdu.Body.(*smpp.Bind)
	if !ok {
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusSysErr, SequenceNumber: pdu.SequenceNumber})
		return
	}

	// dead-carrier in reject_bind mode turns everyone away, regardless of credentials
	// (spec §6.1). This reuses the same ESME_RBINDFAIL + close seam as a bad credential.
	if s.smsc.scenario.RejectBind() {
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusBindFail, SequenceNumber: pdu.SequenceNumber})
		s.state = stateClosed
		return
	}

	creds := s.smsc.cfg.BindCredentials
	idOK := subtle.ConstantTimeCompare([]byte(bind.SystemID), []byte(creds.SystemID)) == 1
	pwOK := subtle.ConstantTimeCompare([]byte(bind.Password), []byte(creds.Password)) == 1
	if !idOK || !pwOK {
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusBindFail, SequenceNumber: pdu.SequenceNumber})
		s.state = stateClosed
		return
	}

	s.state = stateBound
	s.systemID = bind.SystemID
	s.bindType = bindType
	s.canSubmit = pdu.CommandID != smpp.BindReceiver
	s.canReceive = pdu.CommandID != smpp.BindTransmitter
	s.scenarioState = s.smsc.scenario.NewBindState(s.smsc.cfg.Seed, s.smsc.cfg.Name, s.id)
	s.bound.Store(true)
	s.smsc.binds.add(bindInfo{
		id:          s.id,
		systemID:    bind.SystemID,
		bindType:    s.bindType,
		connectedAt: time.Now(),
	})

	s.send(&smpp.PDU{
		CommandID:      respID,
		CommandStatus:  smpp.StatusROK,
		SequenceNumber: pdu.SequenceNumber,
		Body:           &smpp.BindResp{SystemID: s.smsc.cfg.Name},
	})
}

// handleSubmit runs the submit_sm flow: advance the clocks, consult the scenario,
// serve the latency, record the PDU and act on the decided outcome — success (ROK),
// error (a non-ROK status), timeout (withhold the response) or disconnect (drop the
// link). The flow order is the one in CLAUDE.md: decode → scenario → fault → answer.
func (s *session) handleSubmit(pdu *smpp.PDU) {
	if s.state != stateBound || !s.canSubmit {
		s.send(&smpp.PDU{CommandID: smpp.SubmitSMResp, CommandStatus: smpp.StatusInvBndSts, SequenceNumber: pdu.SequenceNumber})
		return
	}

	msg, ok := pdu.Body.(*smpp.Message)
	if !ok {
		s.send(&smpp.PDU{CommandID: smpp.SubmitSMResp, CommandStatus: smpp.StatusSysErr, SequenceNumber: pdu.SequenceNumber})
		return
	}

	// The tick is chosen before serving latency (the scenario keys its outcome and
	// latency on it), but only committed once we are sure to record and answer. If the
	// engine shuts down mid-latency we abandon the submit without advancing either
	// clock, so logical_clock never counts a PDU the recorder never stored (plan §1.5).
	tick := s.perBindClock + 1
	decision := s.smsc.scenario.Evaluate(s.scenarioState, tick)
	if !s.serveLatency(decision.LatencyMS) {
		return // engine shutting down: abandon this submit rather than sleep on
	}

	// Every committed outcome — including timeout and disconnect — advances both clocks
	// and records the PDU, so the per-bind corpus stays reconstructable at the right
	// tick and the recorder (the assertion surface) sees every received submit_sm.
	s.perBindClock = tick
	s.smsc.logicalClock.Add(1)
	// Anchor the quiescence window: the flush fires this long after the last submit_sm.
	// Off the deterministic content path (it decides only WHEN to drain, never what or in
	// what order), so reading the wall clock here is legitimate even in seeded mode.
	s.lastSubmit = time.Now()

	messageID := s.messageID()
	s.smsc.recorder.Append(recorder.RecordedPDU{
		MessageID:    messageID,
		SourceAddr:   msg.SourceAddr,
		SourceTON:    msg.SourceAddrTON,
		SourceNPI:    msg.SourceAddrNPI,
		DestAddr:     msg.DestAddr,
		DestTON:      msg.DestAddrTON,
		DestNPI:      msg.DestAddrNPI,
		DataCoding:   msg.DataCoding,
		ShortMessage: msg.ShortMessage,
		PerBindClock: s.perBindClock,
	})

	switch decision.Outcome {
	case scenario.OutcomeSuccess:
		s.send(&smpp.PDU{
			CommandID:      smpp.SubmitSMResp,
			CommandStatus:  smpp.StatusROK,
			SequenceNumber: pdu.SequenceNumber,
			Body:           &smpp.SubmitResp{MessageID: messageID},
		})
		// A successful submit schedules its DLR (when the profile configures one), anchored
		// to the origin tick + the configured delay on this bind.
		if decision.DLR != nil {
			s.scheduleDLR(messageID, msg, decision.DLR)
		}
	case scenario.OutcomeError:
		// A non-ROK submit_sm_resp carries no message_id body.
		s.send(&smpp.PDU{CommandID: smpp.SubmitSMResp, CommandStatus: decision.Status, SequenceNumber: pdu.SequenceNumber})
	case scenario.OutcomeTimeout:
		// Withhold the response entirely; readLoop keeps reading so the client can send
		// more (its own response_timeout fires eventually).
		return
	case scenario.OutcomeDisconnect:
		if decision.DisconnectWhen == config.DisconnectAfterResponse {
			s.send(&smpp.PDU{
				CommandID:      smpp.SubmitSMResp,
				CommandStatus:  smpp.StatusROK,
				SequenceNumber: pdu.SequenceNumber,
				Body:           &smpp.SubmitResp{MessageID: messageID},
			})
		}
		s.state = stateClosed // teardown closes the TCP connection
		return
	}

	// Normal drain (voie a): this submit advanced the clock, so release any DLRs whose
	// due tick it has now reached, in deterministic tick order.
	s.drainDue(s.perBindClock)
}

// serveLatency waits the served latency, returning false if the engine is shutting
// down (so the caller abandons the response). The delay's *value* is deterministic;
// the wait itself is real time, as any served latency must be.
func (s *session) serveLatency(ms uint64) bool {
	if ms == 0 {
		return true
	}
	if ms > maxServedLatencyMS {
		ms = maxServedLatencyMS // a test peer never serves more than a day of latency
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-s.quit:
		return false
	case <-timer.C:
		return true
	}
}

// messageID mints the deterministic id returned in submit_sm_resp and later
// referenced by the correlated DLR (plan §6 decision): the bind ordinal and the
// per-bind tick, both reproducible at a fixed seed. It is a simulator convention,
// not an SMPP concept, so it lives here rather than in the codec.
func (s *session) messageID() string {
	return fmt.Sprintf("%d-%04d", s.id, s.perBindClock)
}

// bindKind maps a bind command to its human-readable type name and the matching
// response command id, from a single switch so the two can never drift apart.
func bindKind(id smpp.CommandID) (name string, respID smpp.CommandID) {
	switch id {
	case smpp.BindTransmitter:
		return "transmitter", smpp.BindTransmitterResp
	case smpp.BindReceiver:
		return "receiver", smpp.BindReceiverResp
	default:
		return "transceiver", smpp.BindTransceiverResp
	}
}
