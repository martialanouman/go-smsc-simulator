package smpp

import (
	"bytes"
	"testing"
)

func TestNewMobileOriginated_IsNotAReceipt(t *testing.T) {
	pdu := NewMobileOriginated("33699999999", "33600000000", "hello")
	if pdu.CommandID != DeliverSM {
		t.Fatalf("command_id = %s, want deliver_sm", pdu.CommandID)
	}
	m, ok := pdu.Body.(*Message)
	if !ok {
		t.Fatalf("body = %T, want *Message", pdu.Body)
	}
	// esm_class 0 is the whole point: it distinguishes an MO from a 0x04 delivery receipt.
	if m.ESMClass != 0 {
		t.Errorf("esm_class = 0x%02x, want 0x00", m.ESMClass)
	}
	if len(m.TLVs) != 0 {
		t.Errorf("MO carries %d TLVs, want 0 (receipt-only correlation TLVs must not appear)", len(m.TLVs))
	}
	if string(m.ShortMessage) != "hello" {
		t.Errorf("short_message = %q, want %q", m.ShortMessage, "hello")
	}
}

func TestNewMobileOriginated_RoundTrip(t *testing.T) {
	const (
		source  = "33699999999"
		dest    = "33600000000"
		content = "an uplink message"
	)
	frame, err := Encode(NewMobileOriginated(source, dest, content))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	pdu, err := Decode(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pdu.CommandID != DeliverSM {
		t.Fatalf("decoded command_id = %s, want deliver_sm", pdu.CommandID)
	}
	m := pdu.Body.(*Message)
	if m.ESMClass != 0 {
		t.Errorf("decoded esm_class = 0x%02x, want 0x00", m.ESMClass)
	}
	if m.SourceAddr != source || m.DestAddr != dest {
		t.Errorf("decoded addrs = %s→%s, want %s→%s", m.SourceAddr, m.DestAddr, source, dest)
	}
	if !bytes.Equal(m.ShortMessage, []byte(content)) {
		t.Errorf("decoded short_message = %q, want %q", m.ShortMessage, content)
	}
}
