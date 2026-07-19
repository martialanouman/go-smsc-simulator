package smpp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// sampleResp is a well-formed submit_sm_resp, the PDU a virtual SMSC would normally
// answer with and the one edge-case injection corrupts.
func sampleResp() *PDU {
	return &PDU{
		CommandID:      SubmitSMResp,
		CommandStatus:  StatusROK,
		SequenceNumber: 42,
		Body:           &SubmitResp{MessageID: "7-0001"},
	}
}

// TestEncodeEdgeCase_BadLength: the header overstates command_length, so framing the
// bytes back off a stream fails — the peer waits for bytes that never come.
func TestEncodeEdgeCase_BadLength(t *testing.T) {
	t.Parallel()

	clean, err := Encode(sampleResp())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	realLen := binary.BigEndian.Uint32(clean[0:4])

	frame, err := EncodeEdgeCase(sampleResp(), EdgeBadLength)
	if err != nil {
		t.Fatalf("EncodeEdgeCase: %v", err)
	}
	claimed := binary.BigEndian.Uint32(frame[0:4])
	if claimed != realLen+badLengthInflation {
		t.Fatalf("command_length = %d, want real+%d = %d", claimed, badLengthInflation, realLen+badLengthInflation)
	}
	if _, err := ReadPDU(bytes.NewReader(frame)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadPDU error = %v, want unexpected EOF (header claims more bytes than present)", err)
	}
}

// TestEncodeEdgeCase_UnknownCmdID: the frame is well-framed but carries a command_id
// the decoder does not model, so Decode rejects it with ErrUnknownCommand.
func TestEncodeEdgeCase_UnknownCmdID(t *testing.T) {
	t.Parallel()

	frame, err := EncodeEdgeCase(sampleResp(), EdgeUnknownCmdID)
	if err != nil {
		t.Fatalf("EncodeEdgeCase: %v", err)
	}
	if _, err := Decode(frame); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("Decode error = %v, want ErrUnknownCommand", err)
	}
}

// TestEncodeEdgeCase_BadSequence: the frame stays decodable, but its sequence_number
// is shifted off the request it answers, so the peer cannot correlate the response.
func TestEncodeEdgeCase_BadSequence(t *testing.T) {
	t.Parallel()

	orig := sampleResp()
	frame, err := EncodeEdgeCase(orig, EdgeBadSequence)
	if err != nil {
		t.Fatalf("EncodeEdgeCase: %v", err)
	}
	got, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if want := orig.SequenceNumber + badSequenceOffset; got.SequenceNumber != want {
		t.Fatalf("sequence_number = %d, want %d (shifted out of order)", got.SequenceNumber, want)
	}
}

// TestEncodeEdgeCase_Deterministic: the same PDU and kind always produce identical
// bytes — injection must be reproducible without any PRNG (invariant a).
func TestEncodeEdgeCase_Deterministic(t *testing.T) {
	t.Parallel()

	for _, kind := range []EdgeCaseKind{EdgeBadLength, EdgeUnknownCmdID, EdgeBadSequence} {
		a, err := EncodeEdgeCase(sampleResp(), kind)
		if err != nil {
			t.Fatalf("EncodeEdgeCase(%s): %v", kind, err)
		}
		b, err := EncodeEdgeCase(sampleResp(), kind)
		if err != nil {
			t.Fatalf("EncodeEdgeCase(%s): %v", kind, err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("kind %s not deterministic: %x != %x", kind, a, b)
		}
	}
}
