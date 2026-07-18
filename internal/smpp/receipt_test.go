package smpp

import (
	"bytes"
	"strings"
	"testing"
)

func TestReceiptText_Format(t *testing.T) {
	got := ReceiptText(DeliveryReceipt{
		MessageID:  "1-0007",
		State:      StateDelivered,
		ErrorCode:  "000",
		SubmitDate: "2507180000",
		DoneDate:   "2507180001",
		Text:       "hello world",
	})
	want := "id:1-0007 sub:001 dlvrd:001 submit date:2507180000 done date:2507180001 stat:DELIVRD err:000 text:hello world"
	if got != want {
		t.Fatalf("receipt text\n got: %q\nwant: %q", got, want)
	}
}

func TestReceiptText_StatAndDlvrdByState(t *testing.T) {
	cases := []struct {
		state MessageState
		stat  string
		dlvrd string
	}{
		{StateDelivered, "stat:DELIVRD", "dlvrd:001"},
		{StateUndeliverable, "stat:UNDELIV", "dlvrd:000"},
		{StateExpired, "stat:EXPIRED", "dlvrd:000"},
	}
	for _, c := range cases {
		txt := ReceiptText(DeliveryReceipt{MessageID: "x", State: c.state, ErrorCode: "001"})
		if !strings.Contains(txt, c.stat) {
			t.Errorf("state %d: %q missing %q", c.state, txt, c.stat)
		}
		if !strings.Contains(txt, c.dlvrd) {
			t.Errorf("state %d: %q missing %q", c.state, txt, c.dlvrd)
		}
	}
}

func TestReceiptText_TruncatesTextTo20(t *testing.T) {
	long := "0123456789ABCDEFGHIJKLMNOP" // 26 chars
	txt := ReceiptText(DeliveryReceipt{MessageID: "x", State: StateDelivered, Text: long})
	if !strings.HasSuffix(txt, "text:0123456789ABCDEFGHIJ") { // first 20 chars
		t.Fatalf("text not truncated to 20: %q", txt)
	}
}

// TestNewDeliveryReceipt_RoundTrip builds a receipt, encodes it, and decodes it back —
// asserting the deliver_sm carries esm_class 0x04, swapped addresses, the receipt text,
// and both correlation TLVs. This exercises the real codec both ways.
func TestNewDeliveryReceipt_RoundTrip(t *testing.T) {
	r := DeliveryReceipt{
		MessageID:     "1-0042",
		State:         StateDelivered,
		ErrorCode:     "000",
		SubmitDate:    "2507180000",
		DoneDate:      "2507180005",
		Text:          "ping",
		SourceAddr:    "33700000002", // origin dest
		SourceAddrTON: 1,
		SourceAddrNPI: 1,
		DestAddr:      "33600000001", // origin source
		DestAddrTON:   1,
		DestAddrNPI:   1,
	}

	frame, err := Encode(NewDeliveryReceipt(r))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	pdu, err := Decode(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if pdu.CommandID != DeliverSM {
		t.Fatalf("command = %v, want DeliverSM", pdu.CommandID)
	}
	m, ok := pdu.Body.(*Message)
	if !ok {
		t.Fatalf("body type = %T, want *Message", pdu.Body)
	}
	if m.ESMClass != ESMClassDeliveryReceipt {
		t.Errorf("esm_class = 0x%02x, want 0x04", m.ESMClass)
	}
	if m.SourceAddr != "33700000002" || m.DestAddr != "33600000001" {
		t.Errorf("addresses not swapped: src=%q dst=%q", m.SourceAddr, m.DestAddr)
	}
	if got := string(m.ShortMessage); !strings.Contains(got, "id:1-0042") || !strings.Contains(got, "stat:DELIVRD") {
		t.Errorf("short_message missing receipt fields: %q", got)
	}

	var sawID, sawState bool
	for _, tlv := range m.TLVs {
		switch tlv.Tag {
		case TagReceiptedMessageID:
			sawID = true
			if !bytes.Equal(tlv.Value, cOctetBytes("1-0042")) {
				t.Errorf("receipted_message_id = %q, want C-octet 1-0042", tlv.Value)
			}
		case TagMessageState:
			sawState = true
			if len(tlv.Value) != 1 || MessageState(tlv.Value[0]) != StateDelivered {
				t.Errorf("message_state TLV = %v, want [2]", tlv.Value)
			}
		}
	}
	if !sawID || !sawState {
		t.Errorf("missing correlation TLVs: id=%v state=%v", sawID, sawState)
	}
}
