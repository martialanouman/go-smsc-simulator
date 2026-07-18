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

	"github.com/martialanouman/go-smsc-simulator/internal/recorder"
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
	state        sessionState
	bindType     string
	canSubmit    bool
	systemID     string
	perBindClock uint64

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
	}
}

// run owns the whole session lifetime: it starts the writer and a closer that drops
// the connection on engine shutdown, runs the read loop, then tears everything down
// in order so a queued response (e.g. unbind_resp) still reaches the wire before the
// socket closes.
func (s *session) run() {
	done := make(chan struct{})
	defer close(done)

	// closer: on engine shutdown, unbind bound clients gracefully rather than dropping
	// the TCP connection under them (CLAUDE.md: "unbind propre des binds sur SIGTERM").
	// It queues the unbind, then sets a past read deadline to unblock readLoop; teardown
	// then flushes the queued unbind before closing the socket. Best-effort: it does not
	// wait for unbind_resp.
	go func() {
		select {
		case <-s.quit:
			if s.bound.Load() {
				s.send(&smpp.PDU{CommandID: smpp.Unbind, SequenceNumber: serverInitiatedSeq})
			}
			_ = s.conn.SetReadDeadline(time.Now())
		case <-done:
		}
	}()

	go s.writeLoop()

	s.readLoop()

	// Teardown order matters: stop accepting writes, let the writer flush what is
	// already queued, then close the socket and deregister the bind.
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

// readLoop reads, decodes and handles PDUs until the connection ends or a handler
// closes the session. A decode failure is answered with generic_nack rather than
// dropping the link, echoing the sequence number straight from the frame.
func (s *session) readLoop() {
	for {
		frame, err := smpp.ReadPDU(s.conn)
		if err != nil {
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
// serve the latency, record the PDU and answer. At S2 the scenario is healthy, so
// the outcome is always ESME_ROK; error/timeout/disconnect arrive at S3 (plan §7).
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
	decision := s.smsc.scenario.Evaluate(tick)
	if !s.serveLatency(decision.LatencyMS) {
		return // engine shutting down: abandon this submit rather than sleep on
	}

	s.perBindClock = tick
	s.smsc.logicalClock.Add(1)

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

	s.send(&smpp.PDU{
		CommandID:      smpp.SubmitSMResp,
		CommandStatus:  smpp.StatusROK,
		SequenceNumber: pdu.SequenceNumber,
		Body:           &smpp.SubmitResp{MessageID: messageID},
	})
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
