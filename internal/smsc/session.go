package smsc

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
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

// maxQuiescenceFlushMS bounds the quiescence window before its millisecond→Duration
// conversion; it matches the config-layer ceiling on quiescence_flush_ms.
const maxQuiescenceFlushMS = 600_000

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
	// currentEngine is the profile this bind currently evaluates submits against. It starts
	// at the SMSC's initial engine and is swapped by a scheduled transition — the only path
	// that moves the active profile. It also sources the served-latency label (via
	// currentEngine.Profile()), so latency is attributed to THIS bind's active scenario
	// rather than the SMSC-global activeProfile (a last-writer value under concurrent
	// per-bind transitions). Owned by readLoop, so no lock.
	currentEngine *scenario.Engine
	// transitions is this bind's scheduled profile switches, sorted by at_tick; the cursor
	// advances with the logical clock (applyDueTransitions). Kept off the Schedule Runner on
	// purpose — a transition is latent config, never quiescence-flushed (see schedule_events).
	transitions      []config.ScheduledTransition
	transitionCursor int
	// disconnects is this bind's scheduled_disconnects, sorted by at_tick, consumed the same
	// way (applyDueDisconnects) and for the same reason: never quiescence-flushed.
	disconnects      []config.ScheduledDisconnect
	disconnectCursor int

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

	outbound     chan outboundPDU
	writerClosed chan struct{}
	// writeErr is set by writeLoop before it closes writerClosed, so it is safe to read
	// once writerClosed is observed closed.
	writeErr error

	// Close attribution, all owned by the read goroutine except where noted: why the
	// session ends (closeWith), the deadline armed for the current read (to tell our own
	// deadline from a peer failure mid-frame), and the counters the close record reports.
	openedAt     time.Time
	closeReason  closeReason
	closeErr     error
	readDeadline time.Time
	pdusRead     uint64
	pdusWritten  uint64        // owned by writeLoop, read after writerClosed
	pdusDropped  atomic.Uint64 // any goroutine that queues

	// deferred tracks the goroutines holding a response back for its served latency (and
	// the DLRs waiting on those responses). Teardown closes stopDeferred and waits on them
	// before closing outbound, so none can ever send on a closed channel.
	deferred     sync.WaitGroup
	stopDeferred chan struct{}
}

func newSession(conn net.Conn, v *virtualSMSC, quit <-chan struct{}) *session {
	quiescenceMS := v.cfg.EffectiveQuiescenceFlushMs()
	if quiescenceMS > maxQuiescenceFlushMS {
		quiescenceMS = maxQuiescenceFlushMS // validated at load, clamped here so the conversion cannot overflow
	}
	return &session{
		conn:         conn,
		smsc:         v,
		quit:         quit,
		logger:       v.logger,
		outbound:     make(chan outboundPDU, 8),
		writerClosed: make(chan struct{}),
		stopDeferred: make(chan struct{}),
		quiescence:   time.Duration(quiescenceMS) * time.Millisecond,
		openedAt:     time.Now(), // telemetry only (session lifetime), never a decision input
	}
}

// Outbound kinds, the kind label of smsc_outbound_dropped_total: a delivery receipt, a
// mobile-originated message, or anything else the server writes (responses and control).
const (
	kindResp = "resp"
	kindDLR  = "dlr"
	kindMO   = "mo"
)

// outboundPDU is one encoded PDU queued for the writer, tagged with its kind.
type outboundPDU struct {
	b    []byte
	kind string
}

// closeWith ends the session for reason: readLoop returns once it sees stateClosed, and
// teardown records the close.
func (s *session) closeWith(reason closeReason, err error) {
	s.state = stateClosed
	s.closeReason = reason
	s.closeErr = err
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
		defer s.recoverGoroutine("closer") // runs before close(closerDone) on panic, so teardown still unblocks
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

	// Teardown is deferred so it runs even if readLoop panics: the panic is recovered one
	// frame up (engine.recoverSession), but only AFTER this releases the connection, joins
	// the writer and deregisters the bind. Without the defer, a recovered panic would leak
	// the writeLoop goroutine (blocked on an unclosed outbound), the FD and the bind — the
	// opposite of the crash isolation S6/T1 promises.
	//
	// Order matters. First stop the closer and wait until it can no longer call send (a
	// session can end on its own — a disconnect outcome, a client hang-up — while the
	// engine shuts down concurrently; without this the closer could send on a closed
	// outbound and panic). Only then close outbound, flush the writer, close the socket
	// and deregister the bind.
	defer func() {
		// A writer that already exited failed on its own, BEFORE the read side ended: that
		// write failure is the cause, and whatever the read side saw next is a consequence.
		writerFailedFirst := isClosed(s.writerClosed)
		queueDepth := len(s.outbound)
		close(done)
		<-closerDone
		// Responses still inside their served latency are dropped with the link.
		close(s.stopDeferred)
		s.deferred.Wait()
		close(s.outbound)
		<-s.writerClosed
		for p := range s.outbound { // left behind by a failed writer
			s.smsc.metrics.AddOutboundDepth(s.smsc.cfg.Name, -1)
			s.drop(p.kind)
		}
		_ = s.conn.Close()
		s.recordClose(writerFailedFirst, queueDepth)
		s.smsc.binds.remove(s.id)
		// Balance the IncBind on a successful bind. A rejected bind never set bound, so the
		// active-binds gauge only ever counts binds that actually registered.
		if s.bound.Load() {
			s.smsc.metrics.DecBind(s.smsc.cfg.Name, s.bindType)
		}
	}()

	s.readLoop()
}

// writeLoop is the sole writer of the connection. It drains outbound until the
// channel is closed (clean teardown) or a write fails (broken peer).
func (s *session) writeLoop() {
	defer close(s.writerClosed)
	defer s.recoverGoroutine("writeLoop") // runs before close(s.writerClosed) on panic, so teardown still unblocks
	for p := range s.outbound {
		s.smsc.metrics.AddOutboundDepth(s.smsc.cfg.Name, -1)
		// A write deadline per write: a client that stopped reading must not wedge this
		// goroutine indefinitely — the deadline turns it into a write error and teardown.
		_ = s.conn.SetWriteDeadline(time.Now().Add(s.smsc.writeTimeout))
		if _, err := s.conn.Write(p.b); err != nil {
			s.writeErr = err
			s.drop(p.kind)
			s.logger.Warn("session writer failed; outbound PDUs are dropped until the session closes",
				slog.String("reason", string(writeFailure(err))), slog.Any("err", err))
			return
		}
		s.pdusWritten++
	}
}

// drop counts one outbound PDU discarded because the writer had failed.
func (s *session) drop(kind string) {
	s.pdusDropped.Add(1)
	s.smsc.metrics.IncOutboundDropped(s.smsc.cfg.Name, kind)
}

// isClosed reports whether ch is closed, without blocking.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// recordClose emits the one metric sample and log record of this session's end. The
// reason is the one closeWith set; if none was set, readLoop panicked. A writer that failed
// before the read side ended takes precedence: it is the first cause (a client that stops
// reading, then hangs up, closed on write_timeout — not on client_eof). It runs after the
// writer is joined, so writeErr and pdusWritten are safe to read.
func (s *session) recordClose(writerFailedFirst bool, queueDepth int) {
	reason, err := s.closeReason, s.closeErr
	if reason == "" {
		reason = reasonPanic
	}
	if writerFailedFirst && s.writeErr != nil {
		reason, err = writeFailure(s.writeErr), s.writeErr
	}
	bindType := s.bindType
	if bindType == "" {
		bindType = "none" // closed before any bind attempt (a bare TCP probe)
	}
	initiator := reason.initiator()
	s.smsc.metrics.IncSessionClosed(s.smsc.cfg.Name, bindType, string(reason), initiator)

	attrs := []any{
		slog.String("reason", string(reason)),
		slog.String("initiator", initiator),
		slog.String("bind_type", bindType),
		slog.String("system_id", s.systemID),
		slog.String("remote_addr", s.conn.RemoteAddr().String()),
		slog.Any("err", err),
		slog.Duration("lifetime", time.Since(s.openedAt)),
		slog.Uint64("pdus_read", s.pdusRead),
		slog.Uint64("pdus_written", s.pdusWritten),
		slog.Uint64("pdus_dropped", s.pdusDropped.Load()),
		slog.Int("outbound_queue_depth", queueDepth),
		slog.Int("pending_scheduled", s.sched.Len()),
	}
	if initiator == "server" {
		s.logger.Warn("session closed", attrs...)
	} else {
		s.logger.Debug("session closed", attrs...)
	}
}

// send encodes and queues a PDU for the writer. It never blocks the read goroutine
// past the writer's life: if the writer has gone, the PDU is dropped rather than
// deadlocking on a full channel.
func (s *session) send(pdu *smpp.PDU) {
	s.sendKind(pdu, kindResp)
}

// sendKind is send for a PDU whose kind is not a response (a DLR or MO deliver_sm).
func (s *session) sendKind(pdu *smpp.PDU, kind string) {
	if b := s.encode(pdu, nil); b != nil {
		s.sendBytes(b, kind)
	}
}

// encode encodes pdu, honouring an optional edge-case plan: with a plan the PDU is
// deliberately malformed (protocol_edge_cases), otherwise it is encoded strictly — the
// one seam where injection reaches the wire. An encode failure is logged and yields nil.
func (s *session) encode(pdu *smpp.PDU, edge *scenario.EdgeCasePlan) []byte {
	var (
		b   []byte
		err error
	)
	if edge == nil {
		b, err = smpp.Encode(pdu)
	} else {
		b, err = smpp.EncodeEdgeCase(pdu, edge.Kind)
	}
	if err != nil {
		s.logger.Error("encode pdu", slog.String("command", pdu.CommandID.String()), slog.Any("error", err))
		return nil
	}
	return b
}

// respond queues an encoded submit_sm_resp after its served latency. A zero latency goes
// out inline; otherwise a goroutine holds it back so readLoop keeps serving the rest of
// the client's window (and its enquire_links) meanwhile — responses then complete out of
// order, as on a real SMSC. The returned channel closes once the response is queued (nil
// when it already is), so a DLR can wait on it and never overtake its submit_sm_resp.
func (s *session) respond(b []byte, latencyMS uint64) <-chan struct{} {
	if b == nil || latencyMS == 0 {
		if b != nil {
			s.sendBytes(b, kindResp)
		}
		return nil
	}
	ready, sent := make(chan struct{}), make(chan struct{})
	time.AfterFunc(latencyDuration(latencyMS), func() { close(ready) })
	s.sendWhen(ready, b, kindResp, sent)
	return sent
}

// sendWhen queues b off the read goroutine once ready fires, then closes sent (if
// non-nil). Teardown cancels it through stopDeferred.
func (s *session) sendWhen(ready <-chan struct{}, b []byte, kind string, sent chan struct{}) {
	s.deferred.Add(1)
	go func() {
		defer s.deferred.Done()
		defer s.recoverGoroutine("deferred send")
		select {
		case <-ready:
			s.sendBytes(b, kind)
			if sent != nil {
				close(sent)
			}
		case <-s.stopDeferred:
		}
	}()
}

// sendBytes queues already-encoded bytes for the writer. While the writer lives, a full
// queue blocks the caller: backpressure, bounded by the writer's writeTimeout. Once the
// writer has failed, the bytes are dropped and counted rather than deadlocking.
func (s *session) sendBytes(b []byte, kind string) {
	s.smsc.metrics.AddOutboundDepth(s.smsc.cfg.Name, 1) // before the send, so the writer's -1 never runs first
	select {
	case s.outbound <- outboundPDU{b: b, kind: kind}:
	case <-s.writerClosed:
		s.smsc.metrics.AddOutboundDepth(s.smsc.cfg.Name, -1)
		s.drop(kind)
	}
}

// recoverGoroutine is the last-resort panic boundary for this session's writer and
// closer goroutines — the counterpart to engine.recoverSession, which covers only the
// synchronous readLoop. A panic in either goroutine (a future codec/encode bug, say)
// must never escape to crash the process, its accept loop or the sibling virtual SMSCs
// (S6/T1). It logs loudly with the offending goroutine, panic value and stack, then
// lets the goroutine unwind so its deferred channel close still runs and teardown does
// not wedge on it. Deliberately per session — never a global recover that would mask a
// determinism bug across instances (CLAUDE.md "recover de dernier ressort par SMSC virtuel").
func (s *session) recoverGoroutine(where string) {
	if r := recover(); r != nil {
		s.logger.Error("session goroutine panic recovered",
			slog.String("goroutine", where),
			slog.Any("panic", r),
			slog.String("stack", string(debug.Stack())))
	}
}

// armReadDeadline sets the read deadline for the next blocking read. With no events
// pending it is the long idle-reap window; with events pending it is shortened to the
// quiescence window (measured from the last submit) so the flush can fire while the bind
// sits silent — whichever is sooner, so idle-reaping still bounds a truly dead bind.
func (s *session) armReadDeadline() {
	deadline := time.Now().Add(s.smsc.idleTimeout)
	if s.sched.Len() > 0 {
		if q := s.lastSubmit.Add(s.quiescence); q.Before(deadline) {
			deadline = q
		}
	}
	s.readDeadline = deadline
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
			s.closeWith(reasonShutdown, nil)
			return
		default:
		}

		frame, err := smpp.ReadPDU(&frameReader{s: s})
		if err != nil {
			// A read-deadline timeout is not necessarily the end of the session: it is how
			// the quiescence flush is driven. Shutdown takes priority; then, while events
			// remain pending, flush once the idle window has elapsed and keep the bind
			// alive; only a timeout with nothing pending reaps a genuinely silent bind.
			if s.isTimeout(err) {
				select {
				case <-s.quit:
					s.closeWith(reasonShutdown, nil)
					return
				default:
				}
				if s.sched.Len() > 0 {
					if time.Since(s.lastSubmit) >= s.quiescence {
						s.flushSchedule() // voie b: silence past the window drains pending events in tick order
						if s.state == stateClosed {
							return // a flushed scheduled disconnect cut this bind
						}
					}
					continue
				}
				s.closeWith(reasonIdleTimeout, err) // idle timeout with no pending events: reap the silent bind
				return
			}
			s.closeWith(s.readFailure(err), err)
			if s.closeReason == reasonProtocolError {
				// The stream cannot be resynchronised; say why before teardown closes it.
				// No sequence number was read, so it is 0 (SMPP v3.4 §4.3).
				s.send(&smpp.PDU{CommandID: smpp.GenericNack, CommandStatus: smpp.StatusInvCmdLen})
			}
			return
		}
		s.pdusRead++

		pdu, err := smpp.Decode(frame)
		if err != nil {
			s.rejectMalformed(frame, err)
			continue
		}

		s.handle(pdu)
		if s.state == stateClosed {
			return
		}
	}
}

// frameReader feeds ReadPDU one frame. The armed deadline may be the short quiescence
// wake-up, which is only meant to fire BETWEEN frames: once the first byte of a frame has
// arrived, the rest of the frame gets the idle (liveness) window instead. Otherwise a frame
// straddling the wake-up would be cut mid-read, its consumed bytes lost, and the session
// closed on a healthy carrier (the step-280 cuts, partial_frame_timeout).
type frameReader struct {
	s       *session
	started bool
}

func (r *frameReader) Read(p []byte) (int, error) {
	n, err := r.s.conn.Read(p)
	if n > 0 && !r.started {
		r.started = true
		r.s.readDeadline = time.Now().Add(r.s.smsc.idleTimeout)
		_ = r.s.conn.SetReadDeadline(r.s.readDeadline)
		// Shutdown's past deadline (the closer) must win over this extension, whichever ran
		// first: re-check quit after extending, as readLoop does after re-arming.
		select {
		case <-r.s.quit:
			_ = r.s.conn.SetReadDeadline(time.Now())
		default:
		}
	}
	return n, err
}

// readFailure classifies a read that ended the session (a between-frames deadline expiry
// is handled by the caller). It runs on the read goroutine, after the closer may have set
// a past deadline on shutdown, hence the quit check first.
func (s *session) readFailure(err error) closeReason {
	select {
	case <-s.quit:
		return reasonShutdown
	default:
	}
	switch {
	case errors.Is(err, io.EOF):
		return reasonClientEOF
	case errors.Is(err, smpp.ErrBadCommandLength):
		return reasonProtocolError
	case errors.Is(err, smpp.ErrPartialFrame) && !time.Now().Before(s.readDeadline):
		// ErrPartialFrame hides its cause; our own deadline having passed is the signal that
		// the read deadline, not the peer, cut the frame.
		return reasonPartialFrameTimeout
	default:
		// A reset, a truncated frame (io.ErrUnexpectedEOF) or any other transport failure.
		return reasonReadError
	}
}

// rejectMalformed answers a frame that failed to decode, per SMPP v3.4: an unknown
// command_id gets generic_nack; a malformed body on a known request gets that command's
// own _resp with a length status. A malformed response is dropped — a response is never
// answered.
func (s *session) rejectMalformed(frame []byte, err error) {
	id := smpp.CommandID(binary.BigEndian.Uint32(frame[4:8]))
	seq := binary.BigEndian.Uint32(frame[12:16]) // frame is >= 16 bytes (ReadPDU)
	switch {
	case errors.Is(err, smpp.ErrUnknownCommand):
		s.send(&smpp.PDU{CommandID: smpp.GenericNack, CommandStatus: smpp.StatusInvCmdID, SequenceNumber: seq})
	case id.IsResponse():
	default:
		status := smpp.StatusSysErr
		switch {
		case errors.Is(err, smpp.ErrBadShortMessage):
			status = smpp.StatusInvMsgLen
		case errors.Is(err, smpp.ErrTruncated):
			status = smpp.StatusInvCmdLen
		}
		s.send(&smpp.PDU{CommandID: id.Response(), CommandStatus: status, SequenceNumber: seq})
	}
}

// testPanicHook is nil in production. Tests set it to inject a panic into a session
// goroutine (keyed on s.smsc.cfg.Name) to exercise the per-session recover boundary
// (engine.recoverSession) — proving one instance panicking leaves its siblings serving.
// It runs before any dispatch, off the deterministic decision path: it reads neither
// the PRNG nor a clock, so seeded replay is unaffected.
var testPanicHook func(*session)

func (s *session) handle(pdu *smpp.PDU) {
	if testPanicHook != nil {
		testPanicHook(s)
	}

	switch pdu.CommandID {
	case smpp.BindTransmitter, smpp.BindReceiver, smpp.BindTransceiver:
		s.handleBind(pdu)
	case smpp.SubmitSM:
		s.handleSubmit(pdu)
	case smpp.EnquireLink:
		s.send(&smpp.PDU{CommandID: smpp.EnquireLinkResp, CommandStatus: smpp.StatusROK, SequenceNumber: pdu.SequenceNumber})
	case smpp.Unbind:
		s.send(&smpp.PDU{CommandID: smpp.UnbindResp, CommandStatus: smpp.StatusROK, SequenceNumber: pdu.SequenceNumber})
		s.closeWith(reasonClientUnbind, nil)
	case smpp.DeliverSMResp:
		// The ESME acknowledging a DLR (or MO) we emitted. S4 tracks no outbound window,
		// so there is nothing to correlate — accept it silently rather than generic_nack a
		// perfectly valid ack.
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
		s.bindType = bindType // labels the close; the bind itself never registered
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusBindFail, SequenceNumber: pdu.SequenceNumber})
		s.closeWith(reasonBindRejected, nil)
		return
	}

	creds := s.smsc.cfg.BindCredentials
	idOK := subtle.ConstantTimeCompare([]byte(bind.SystemID), []byte(creds.SystemID)) == 1
	pwOK := subtle.ConstantTimeCompare([]byte(bind.Password), []byte(creds.Password)) == 1
	if !idOK || !pwOK {
		s.bindType = bindType
		s.systemID = bind.SystemID // the claimed id, to attribute the refusal
		s.send(&smpp.PDU{CommandID: respID, CommandStatus: smpp.StatusBindFail, SequenceNumber: pdu.SequenceNumber})
		s.closeWith(reasonAuthFailed, nil)
		return
	}

	// The ordinal is taken only now, on a successful bind: a bare TCP connect (k8s
	// tcpSocket probe, wait-for-port) or a rejected bind must not shift it, since it seeds
	// this bind's PRNG and message_ids (invariant a).
	s.id = s.smsc.bindSeq.Add(1)
	s.logger = s.logger.With(slog.Uint64("bind_id", s.id))
	s.state = stateBound
	s.systemID = bind.SystemID
	s.bindType = bindType
	s.canSubmit = pdu.CommandID != smpp.BindReceiver
	s.canReceive = pdu.CommandID != smpp.BindTransmitter
	s.scenarioState = s.smsc.scenario.NewBindState(s.smsc.cfg.Seed, s.smsc.cfg.Name, s.id)
	s.currentEngine = s.smsc.scenario // starts on the initial profile; transitions swap it
	s.smsc.binds.add(bindInfo{
		id:          s.id,
		systemID:    bind.SystemID,
		bindType:    s.bindType,
		connectedAt: time.Now(),
	})
	s.smsc.metrics.IncBind(s.smsc.cfg.Name, s.bindType)
	// Set bound LAST, right after IncBind: teardown gates DecBind on this flag, so making
	// it the final step keeps the active-binds gauge balanced — bound==true now strictly
	// implies IncBind ran (an atomic store cannot panic in the gap), so a panic anywhere
	// earlier tears down without a spurious DecBind driving the gauge negative.
	s.bound.Store(true)

	// Anchor the quiescence window to the bind: S5 events are enqueued here, before any
	// submit_sm, so without this lastSubmit would stay zero and armReadDeadline would fire
	// the flush immediately. Off the deterministic path (it decides only WHEN to drain,
	// never what or in what order), so reading the wall clock here is legitimate even seeded.
	s.lastSubmit = time.Now()
	// Enqueue this bind's tick-anchored schedule (MO/disconnects/transitions) now that it
	// owns a clock and a PRNG state — each bind gets its own copy, keyed to its own clock.
	s.scheduleConfiguredEvents()

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

	tick := s.perBindClock + 1
	// Apply any transition due at this tick before evaluating, so the submit at at_tick runs
	// under the new profile (the switch is keyed to the logical clock, never the wall clock).
	s.applyDueTransitions(tick)
	decision := s.currentEngine.Evaluate(s.scenarioState, tick)
	// A disconnect ends the session, so its latency is served inline (nothing else on this
	// bind is worth serving meanwhile). If the engine shuts down mid-latency the submit is
	// abandoned without advancing either clock, so logical_clock never counts a PDU the
	// recorder never stored (plan §1.5). Every other outcome is decided and committed now;
	// its response alone is held back for the latency (see respond).
	if decision.Outcome == scenario.OutcomeDisconnect && !s.serveLatency(decision.LatencyMS) {
		s.closeWith(reasonShutdown, nil)
		return
	}

	// Every committed outcome — including timeout and disconnect — advances both clocks
	// and records the PDU, so the per-bind corpus stays reconstructable at the right
	// tick and the recorder (the assertion surface) sees every received submit_sm.
	s.perBindClock = tick
	s.smsc.logicalClock.Add(1)
	// Instrument the committed submit: one received count and its served outcome,
	// unconditional (every committed submit is received and resolves to an outcome). The
	// served latency is observed only where a submit_sm_resp actually goes out (see the
	// switch below): timeouts and before-response disconnects never answer, so sampling
	// them would inflate the served-latency percentiles with waits the client never saw.
	// The scenario label is THIS bind's active profile (currentEngine.Profile()), which a
	// transition may have just swapped — never the SMSC-global activeProfile.
	name := s.smsc.cfg.Name
	s.smsc.metrics.IncSubmit(name)
	s.smsc.metrics.IncOutcome(name, outcomeLabel(decision.Outcome))
	observeServed := func() {
		s.smsc.metrics.ObserveServedLatency(name, string(s.currentEngine.Profile()), float64(decision.LatencyMS)/1000)
	}
	resp := func(status smpp.CommandStatus, body smpp.Body) <-chan struct{} {
		observeServed()
		pdu := &smpp.PDU{CommandID: smpp.SubmitSMResp, CommandStatus: status, SequenceNumber: pdu.SequenceNumber, Body: body}
		return s.respond(s.encode(pdu, decision.EdgeCase), decision.LatencyMS)
	}
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

	// A scheduled disconnect due at this tick that fires before_response cuts the link
	// without answering this submit — the same seam as an OutcomeDisconnect before_response.
	if s.dueDisconnectBeforeResponse() {
		s.closeWith(reasonScheduledDisconnect, nil)
		return
	}

	switch decision.Outcome {
	case scenario.OutcomeSuccess:
		// decision.EdgeCase (protocol_edge_cases) malforms this resp when set; nil = strict.
		sent := resp(smpp.StatusROK, &smpp.SubmitResp{MessageID: messageID})
		// A successful submit schedules its DLR (when the profile configures one), anchored
		// to the origin tick + the configured delay on this bind; it waits on sent so it
		// never reaches the client before its submit_sm_resp.
		if decision.DLR != nil {
			s.scheduleDLR(messageID, msg, decision.DLR, sent)
		}
	case scenario.OutcomeError:
		// A non-ROK submit_sm_resp carries no message_id body.
		resp(decision.Status, nil)
	case scenario.OutcomeTimeout:
		// Withhold the response entirely; readLoop keeps reading so the client can send more
		// (its own response_timeout fires eventually). Fall through to the drain: this submit
		// still advanced the clock, so any scheduled MO/disconnect due at this tick must fire —
		// otherwise a pure-timeout profile under continuous traffic would freeze the schedule
		// (the quiescence flush never triggers while submits keep arriving).
	case scenario.OutcomeDisconnect:
		if decision.DisconnectWhen == config.DisconnectAfterResponse {
			s.send(&smpp.PDU{
				CommandID:      smpp.SubmitSMResp,
				CommandStatus:  smpp.StatusROK,
				SequenceNumber: pdu.SequenceNumber,
				Body:           &smpp.SubmitResp{MessageID: messageID},
			})
			observeServed() // a before-response disconnect never answers, so it stays unsampled
		}
		s.closeWith(reasonFaultInjection, nil) // teardown closes the TCP connection
		return
	}

	// Normal drain (voie a): this submit advanced the clock, so release any DLRs/MOs whose
	// due tick it has now reached, in deterministic tick order, then any due disconnect.
	s.drainDue(s.perBindClock)
	s.applyDueDisconnects(s.perBindClock)
}

// serveLatency waits the served latency, returning false if the engine is shutting
// down (so the caller abandons the response). The delay's *value* is deterministic;
// the wait itself is real time, as any served latency must be.
func (s *session) serveLatency(ms uint64) bool {
	if ms == 0 {
		return true
	}
	timer := time.NewTimer(latencyDuration(ms))
	defer timer.Stop()
	select {
	case <-s.quit:
		return false
	case <-timer.C:
		return true
	}
}

// latencyDuration converts a served latency, clamped so the conversion cannot overflow:
// a test peer never serves more than a day of latency.
func latencyDuration(ms uint64) time.Duration {
	if ms > maxServedLatencyMS {
		ms = maxServedLatencyMS
	}
	return time.Duration(ms) * time.Millisecond
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
