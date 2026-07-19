package smpp

import (
	"encoding/binary"
	"fmt"
)

// EdgeCaseKind is one deterministic malformation the server can stamp onto an
// otherwise well-formed response when protocol_edge_cases is enabled (plan §11 / T1).
// Each kind corrupts exactly one header field so a peer's framer/decoder meets a
// specific, reproducible fault — the point is to exercise the gateway-under-test's
// robustness, never to model a real SMSC behaviour.
type EdgeCaseKind uint8

// EdgeCaseKind values. The set mirrors the three malformations spec §6.1 names:
// an invalid command_length, an invalid command_id, and an out-of-order sequence.
const (
	// EdgeBadLength overstates command_length so the peer's framer waits for bytes
	// that never arrive (or mis-frames the next PDU).
	EdgeBadLength EdgeCaseKind = iota
	// EdgeUnknownCmdID replaces command_id with a reserved, unmodelled value.
	EdgeUnknownCmdID
	// EdgeBadSequence shifts sequence_number off the request it answers, so the peer
	// cannot correlate the response — the frame stays otherwise decodable.
	EdgeBadSequence
)

// String renders the kind as its config enum name, for logs and assertions.
func (k EdgeCaseKind) String() string {
	switch k {
	case EdgeBadLength:
		return "bad_length"
	case EdgeUnknownCmdID:
		return "unknown_command_id"
	case EdgeBadSequence:
		return "bad_sequence"
	default:
		return "edge_case(" + fmt.Sprint(uint8(k)) + ")"
	}
}

// Fixed, documented mutation parameters. They are constants (not draws) so an
// edge-case frame is byte-for-byte reproducible from the PDU and kind alone —
// injection stays deterministic without touching any PRNG (invariant a).
const (
	// badLengthInflation is added to the real command_length: a plausible-but-wrong
	// overstatement the peer's framer will mis-read.
	badLengthInflation uint32 = 16
	// unknownCommandID is absent from the modelled command set (see commands.go), so a
	// peer decoding the frame gets ErrUnknownCommand.
	unknownCommandID uint32 = 0x000000FF
	// badSequenceOffset shifts sequence_number out of order by a fixed amount.
	badSequenceOffset uint32 = 0x1000
)

// EncodeEdgeCase encodes p normally, then applies one deterministic mutation to the
// framed bytes. The result is never a frame Decode would accept as the PDU p intended:
// it is the malformed response the server puts on the wire in place of the real one.
// It errors only when Encode does (an over-long body); the header rewrite itself
// cannot fail, since Encode always returns at least a HeaderLen-byte frame.
func EncodeEdgeCase(p *PDU, kind EdgeCaseKind) ([]byte, error) {
	frame, err := Encode(p)
	if err != nil {
		return nil, err
	}
	switch kind {
	case EdgeBadLength:
		// Encode already wrote the true command_length into frame[0:4]; read it back and
		// overstate it, so there is no int->uint32 conversion of the frame length here.
		realLen := binary.BigEndian.Uint32(frame[0:4])
		binary.BigEndian.PutUint32(frame[0:4], realLen+badLengthInflation)
	case EdgeUnknownCmdID:
		binary.BigEndian.PutUint32(frame[4:8], unknownCommandID)
	case EdgeBadSequence:
		seq := binary.BigEndian.Uint32(frame[12:16])
		binary.BigEndian.PutUint32(frame[12:16], seq+badSequenceOffset)
	default:
		return nil, fmt.Errorf("unknown edge-case kind %d", kind)
	}
	return frame, nil
}
