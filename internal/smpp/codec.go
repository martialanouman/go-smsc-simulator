package smpp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HeaderLen is the fixed size of an SMPP PDU header: command_length, command_id,
// command_status and sequence_number, four uint32 fields.
const HeaderLen = 16

// MaxPDULen bounds a single PDU. A real submit_sm is a few hundred bytes; the cap
// is generous but finite so a hostile or corrupt command_length can never make the
// reader allocate an unbounded buffer (fuzz hardening, plan §11 / §13).
const MaxPDULen = 64 * 1024

// maxCOctetLen bounds a null-terminated string scan. No SMPP v3.4 C-Octet field is
// longer than 65 bytes (message_id); the cap stops a missing terminator from
// scanning an attacker-sized field, while staying well above every real maximum.
const maxCOctetLen = 256

// Decode error sentinels. Every malformed input maps to one of these — the decoder
// never panics, so it is safe to point straight at a network socket and to fuzz.
var (
	ErrShortPDU         = errors.New("pdu shorter than header")
	ErrBadCommandLength = errors.New("command_length outside valid range")
	ErrTruncated        = errors.New("pdu body truncated")
	ErrUnterminated     = errors.New("c-octet string not terminated")
	ErrUnknownCommand   = errors.New("unknown command_id")
)

// ReadPDU reads exactly one framed PDU off r and returns its raw bytes (header
// included), ready for Decode. It reads the 4-byte command_length first and
// validates it against [HeaderLen, MaxPDULen] before reading — and therefore
// before allocating — the remainder, so a lying length cannot exhaust memory.
//
// It returns io.EOF when r is cleanly closed between PDUs, so a session read loop
// can distinguish a normal disconnect from a mid-PDU truncation (io.ErrUnexpectedEOF).
func ReadPDU(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length < HeaderLen || length > MaxPDULen {
		return nil, fmt.Errorf("%w: %d", ErrBadCommandLength, length)
	}

	frame := make([]byte, length)
	copy(frame, lenBuf[:])
	if _, err := io.ReadFull(r, frame[4:]); err != nil {
		return nil, err
	}
	return frame, nil
}

// Decode parses a raw framed PDU (as returned by ReadPDU) into a PDU. The body is
// dispatched on command_id; a header-only command yields a nil Body, and an
// unmodelled command_id returns ErrUnknownCommand so the caller can reply with a
// generic_nack rather than crash.
func Decode(frame []byte) (*PDU, error) {
	if len(frame) < HeaderLen {
		return nil, ErrShortPDU
	}

	r := &reader{b: frame}
	// command_length was validated by ReadPDU and is redundant with len(frame);
	// skip it rather than re-checking, so Decode also works on a hand-built frame.
	_, _ = r.u32()
	cmdID, _ := r.u32()
	status, _ := r.u32()
	seq, _ := r.u32()

	p := &PDU{
		CommandID:      CommandID(cmdID),
		CommandStatus:  CommandStatus(status),
		SequenceNumber: seq,
	}

	body, err := decodeBody(p.CommandID, r)
	if err != nil {
		return nil, err
	}
	p.Body = body
	return p, nil
}

func decodeBody(id CommandID, r *reader) (Body, error) {
	switch id {
	case BindReceiver, BindTransmitter, BindTransceiver:
		return decodeBind(r)
	case BindReceiverResp, BindTransmitterResp, BindTransceiverResp:
		return decodeBindResp(r)
	case SubmitSM, DeliverSM:
		return decodeMessage(r)
	case SubmitSMResp, DeliverSMResp:
		return decodeSubmitResp(r)
	case EnquireLink, EnquireLinkResp, Unbind, UnbindResp, GenericNack:
		return nil, nil // header-only commands carry no body
	default:
		return nil, fmt.Errorf("%w: 0x%08x", ErrUnknownCommand, uint32(id))
	}
}

// Encode serialises a PDU, computing command_length from the encoded body so the
// length field can never disagree with the bytes that follow it.
func Encode(p *PDU) ([]byte, error) {
	w := &writer{}
	if p.Body != nil {
		p.Body.marshal(w)
	}
	body := w.buf.Bytes()

	total := HeaderLen + len(body)
	if total > MaxPDULen {
		return nil, fmt.Errorf("%w: encoded %d", ErrBadCommandLength, total)
	}

	out := make([]byte, HeaderLen, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(total))
	binary.BigEndian.PutUint32(out[4:8], uint32(p.CommandID))
	binary.BigEndian.PutUint32(out[8:12], uint32(p.CommandStatus))
	binary.BigEndian.PutUint32(out[12:16], p.SequenceNumber)
	return append(out, body...), nil
}

func decodeBind(r *reader) (Body, error) {
	b := &Bind{}
	var err error
	if b.SystemID, err = r.cOctetString(); err != nil {
		return nil, err
	}
	if b.Password, err = r.cOctetString(); err != nil {
		return nil, err
	}
	if b.SystemType, err = r.cOctetString(); err != nil {
		return nil, err
	}
	if b.InterfaceVersion, err = r.byte(); err != nil {
		return nil, err
	}
	if b.AddrTON, err = r.byte(); err != nil {
		return nil, err
	}
	if b.AddrNPI, err = r.byte(); err != nil {
		return nil, err
	}
	if b.AddressRange, err = r.cOctetString(); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeBindResp(r *reader) (Body, error) {
	b := &BindResp{}
	// An error bind_resp (command_status != ROK) is header-only: the system_id field
	// is present only on success (SMPP v3.4 §4.1.6). No remaining bytes → empty body.
	if r.remaining() == 0 {
		return b, nil
	}
	var err error
	if b.SystemID, err = r.cOctetString(); err != nil {
		return nil, err
	}
	if b.TLVs, err = r.tlvs(); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeMessage(r *reader) (Body, error) {
	m := &Message{}
	var err error
	read := func(dst *string) bool {
		*dst, err = r.cOctetString()
		return err == nil
	}
	readByte := func(dst *uint8) bool {
		*dst, err = r.byte()
		return err == nil
	}

	if !read(&m.ServiceType) || !readByte(&m.SourceAddrTON) || !readByte(&m.SourceAddrNPI) || !read(&m.SourceAddr) {
		return nil, err
	}
	if !readByte(&m.DestAddrTON) || !readByte(&m.DestAddrNPI) || !read(&m.DestAddr) {
		return nil, err
	}
	if !readByte(&m.ESMClass) || !readByte(&m.ProtocolID) || !readByte(&m.PriorityFlag) {
		return nil, err
	}
	if !read(&m.ScheduleDeliveryTime) || !read(&m.ValidityPeriod) {
		return nil, err
	}
	if !readByte(&m.RegisteredDelivery) || !readByte(&m.ReplaceIfPresent) || !readByte(&m.DataCoding) || !readByte(&m.SMDefaultMsgID) {
		return nil, err
	}

	smLength, err := r.byte()
	if err != nil {
		return nil, err
	}
	if m.ShortMessage, err = r.octets(int(smLength)); err != nil {
		return nil, err
	}
	if m.TLVs, err = r.tlvs(); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeSubmitResp(r *reader) (Body, error) {
	s := &SubmitResp{}
	// Like bind_resp, an error submit_sm_resp is header-only: message_id is present
	// only when the submit succeeded (SMPP v3.4 §4.4.2).
	if r.remaining() == 0 {
		return s, nil
	}
	var err error
	if s.MessageID, err = r.cOctetString(); err != nil {
		return nil, err
	}
	return s, nil
}

// reader is a bounded, panic-free cursor over a PDU frame. Every accessor checks
// the remaining length and returns an error rather than slicing out of range, so
// no malformed input can panic the decoder.
type reader struct {
	b   []byte
	pos int
}

// remaining reports how many bytes are left to read — used to tell a header-only
// error response (nothing left) from one carrying a body.
func (r *reader) remaining() int { return len(r.b) - r.pos }

func (r *reader) byte() (uint8, error) {
	if r.pos+1 > len(r.b) {
		return 0, ErrTruncated
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if r.pos+4 > len(r.b) {
		return 0, ErrTruncated
	}
	v := binary.BigEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

// cOctetString reads a null-terminated string, bounded by maxCOctetLen so a
// missing terminator fails fast instead of scanning to the end of a large frame.
func (r *reader) cOctetString() (string, error) {
	limit := min(len(r.b), r.pos+maxCOctetLen)
	for i := r.pos; i < limit; i++ {
		if r.b[i] == 0 {
			s := string(r.b[r.pos:i])
			r.pos = i + 1
			return s, nil
		}
	}
	return "", ErrUnterminated
}

func (r *reader) octets(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, ErrTruncated
	}
	// Copy rather than alias the frame: the caller may retain ShortMessage past
	// the frame's lifetime, and an alias would pin the whole PDU buffer.
	out := make([]byte, n)
	copy(out, r.b[r.pos:r.pos+n])
	r.pos += n
	return out, nil
}

// tlvs parses the trailing optional parameters: every remaining byte is consumed
// as a sequence of tag/length/value triples. A truncated triple is an error.
func (r *reader) tlvs() ([]TLV, error) {
	var out []TLV
	for r.pos < len(r.b) {
		if r.pos+4 > len(r.b) {
			return nil, ErrTruncated
		}
		tag := binary.BigEndian.Uint16(r.b[r.pos:])
		length := int(binary.BigEndian.Uint16(r.b[r.pos+2:]))
		r.pos += 4
		val, err := r.octets(length)
		if err != nil {
			return nil, err
		}
		out = append(out, TLV{Tag: tag, Value: val})
	}
	return out, nil
}

// writer accumulates an encoded body. It cannot fail: bytes.Buffer grows as needed
// and length fields are bounded by MaxPDULen at the Encode boundary.
type writer struct {
	buf bytes.Buffer
}

func (w *writer) byte(b uint8) { w.buf.WriteByte(b) }

func (w *writer) octets(b []byte) { w.buf.Write(b) }

func (w *writer) cOctetString(s string) {
	w.buf.WriteString(s)
	w.buf.WriteByte(0)
}

func (w *writer) tlvs(tlvs []TLV) {
	var hdr [4]byte
	for _, t := range tlvs {
		// A TLV length is a uint16; a value longer than that cannot be represented,
		// and MaxPDULen bounds it well below in practice. Clamp defensively.
		n := len(t.Value)
		if n > maxTLVValueLen {
			n = maxTLVValueLen
		}
		binary.BigEndian.PutUint16(hdr[0:2], t.Tag)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		w.buf.Write(hdr[:])
		w.buf.Write(t.Value[:n])
	}
}

// maxTLVValueLen is the largest value a TLV's uint16 length field can address.
const maxTLVValueLen = 0xFFFF
