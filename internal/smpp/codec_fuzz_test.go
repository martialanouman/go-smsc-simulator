package smpp_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// FuzzReadPDU points the framer straight at hostile input: the decoder sits behind a
// network socket, so ReadPDU must never panic and never allocate an unbounded buffer
// (plan §11 / T2). It reads command_length first and is the sole allocator (codec.go),
// so the property to hold is simply: any frame it returns is within [HeaderLen, MaxPDULen].
// A crasher that violates that reveals a missing bound to add in codec.go, then to pin
// here as a permanent regression seed under testdata/fuzz.
func FuzzReadPDU(f *testing.F) {
	seeds := [][]byte{
		nil,                       // clean EOF
		{0, 0, 0, 8, 0, 0, 0, 0},  // command_length below header
		{0xFF, 0xFF, 0xFF, 0xFF},  // command_length above MaxPDULen
		{0, 0, 0, 32, 0, 0, 0, 4}, // header claims a body that never arrives
		enquireLinkFrame(),        // one well-framed header-only PDU
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := smpp.ReadPDU(bytes.NewReader(data))
		if err != nil {
			return // any error is fine; the contract is "no panic, no over-read"
		}
		if len(frame) < smpp.HeaderLen || len(frame) > smpp.MaxPDULen {
			t.Fatalf("ReadPDU returned a %d-byte frame, outside [%d, %d]",
				len(frame), smpp.HeaderLen, smpp.MaxPDULen)
		}
	})
}

// FuzzDecode drives the body parser with arbitrary well-formed-length frames: Decode
// must never panic on any input (the contract session.readLoop relies on to answer a
// bad PDU with generic_nack instead of crashing). On a frame it accepts, decoding is
// also a fixed point — Decode∘Encode∘Decode reproduces the same PDU — so a hostile
// frame can never decode into a value that silently re-encodes to something else.
func FuzzDecode(f *testing.F) {
	// Valid PDUs, seeded via their canonical encoding, so the corpus starts on the
	// accept path rather than only exercising the reject path.
	for _, p := range []smpp.PDU{
		{CommandID: smpp.BindTransceiver, SequenceNumber: 1, Body: &smpp.Bind{SystemID: "id", Password: "pw", InterfaceVersion: 0x34}},
		{CommandID: smpp.SubmitSM, SequenceNumber: 2, Body: &smpp.Message{SourceAddr: "1", DestAddr: "2", ShortMessage: []byte("hi"), TLVs: []smpp.TLV{{Tag: 0x0424, Value: []byte("x")}}}},
		{CommandID: smpp.SubmitSMResp, CommandStatus: smpp.StatusROK, SequenceNumber: 2, Body: &smpp.SubmitResp{MessageID: "7-0001"}},
		{CommandID: smpp.EnquireLink, SequenceNumber: 3},
		{CommandID: smpp.GenericNack, CommandStatus: smpp.StatusInvCmdID, SequenceNumber: 4},
	} {
		if b, err := smpp.Encode(&p); err == nil {
			f.Add(b)
		}
	}
	// Malformed-but-well-framed frames, so the reject branches are seeded too.
	for _, b := range [][]byte{
		{0, 0, 0, 4}, // shorter than the header
		{0, 0, 0, 16, 0, 0, 0, 0x7F, 0, 0, 0, 0, 0, 0, 0, 1},                  // unknown command_id
		{0, 0, 0, 20, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1, 'a', 'b', 'c', 'd'}, // unterminated system_id
	} {
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, frame []byte) {
		pdu, err := smpp.Decode(frame)
		if err != nil {
			return // a rejected frame is a valid outcome; the contract is "no panic"
		}

		// Decode accepted the frame: re-encoding and re-decoding must land on the same
		// PDU. A re-encode over MaxPDULen is not a decode bug (a huge unframed frame can
		// decode into an over-long body), so treat that as out of scope rather than fail.
		reencoded, err := smpp.Encode(pdu)
		if err != nil {
			return
		}
		pdu2, err := smpp.Decode(reencoded)
		if err != nil {
			t.Fatalf("re-decode of a re-encoded PDU failed: %v\npdu: %#v", err, pdu)
		}
		if !reflect.DeepEqual(pdu, pdu2) {
			t.Fatalf("decode is not stable under re-encode\n first: %#v\nsecond: %#v", pdu, pdu2)
		}
	})
}

// enquireLinkFrame is the canonical encoding of a single enquire_link, used as a
// well-formed seed for the framer fuzz.
func enquireLinkFrame() []byte {
	b, err := smpp.Encode(&smpp.PDU{CommandID: smpp.EnquireLink, SequenceNumber: 1})
	if err != nil {
		panic(err) // a constant PDU that fails to encode is a build-time bug, not a fuzz input
	}
	return b
}
