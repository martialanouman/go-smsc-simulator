package smsc_test

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// SMPP v3.4: a malformed body on a known request is answered with that command's own
// _resp and a length status; generic_nack is reserved for an unknown command_id or an
// invalid command_length (after which the stream cannot be resynchronised).
func TestE2E_MalformedPDUReplies(t *testing.T) {
	t.Parallel()
	h := start(t, "carrier-malformed")

	exchange := func(t *testing.T, frame []byte) *smpp.PDU {
		t.Helper()
		conn, err := net.Dial("tcp", h.smppAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
		raw, err := smpp.ReadPDU(conn)
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		p, err := smpp.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	hdr := func(length, id, seq uint32) []byte {
		b := make([]byte, 16)
		binary.BigEndian.PutUint32(b[0:], length)
		binary.BigEndian.PutUint32(b[4:], id)
		binary.BigEndian.PutUint32(b[12:], seq)
		return b
	}

	// submit_sm whose body stops after two bytes: truncated.
	truncated := append(hdr(18, uint32(smpp.SubmitSM), 7), 0, 0)
	if p := exchange(t, truncated); p.CommandID != smpp.SubmitSMResp || p.CommandStatus != smpp.StatusInvCmdLen || p.SequenceNumber != 7 {
		t.Errorf("truncated submit_sm -> %v status %v seq %d, want submit_sm_resp ESME_RINVCMDLEN seq 7", p.CommandID, p.CommandStatus, p.SequenceNumber)
	}
	// command_length below the header size.
	if p := exchange(t, hdr(8, uint32(smpp.EnquireLink), 9)); p.CommandID != smpp.GenericNack || p.CommandStatus != smpp.StatusInvCmdLen {
		t.Errorf("bad command_length -> %v status %v, want generic_nack ESME_RINVCMDLEN", p.CommandID, p.CommandStatus)
	}
}
