package smpp

import "fmt"

// ESMClassDeliveryReceipt is the esm_class value marking a deliver_sm as an SMSC
// delivery receipt rather than a mobile-originated message (SMPP v3.4 §5.2.12, bits 5-2
// = 0b0100). The ESME reads it to route the PDU to its DLR handler.
const ESMClassDeliveryReceipt uint8 = 0x04

// Optional-parameter tags carried on a delivery receipt (SMPP v3.4 §5.3.2). Emitted in
// addition to the short_message receipt text so an ESME that reads either form correlates
// the receipt back to its submit.
const (
	// TagReceiptedMessageID (§5.3.2.28) carries the message_id of the receipted submit as
	// a C-Octet String — the TLV twin of the "id:" field in the receipt text.
	TagReceiptedMessageID uint16 = 0x001E
	// TagMessageState (§5.3.2.35) carries the final MessageState as a single octet.
	TagMessageState uint16 = 0x0427
)

// MessageState is the final delivery state of a message (SMPP v3.4 §5.2.28), carried in
// the message_state TLV and mirrored by the receipt text's "stat:" field.
type MessageState uint8

// MessageState values.
const (
	StateEnroute       MessageState = 1
	StateDelivered     MessageState = 2
	StateExpired       MessageState = 3
	StateDeleted       MessageState = 4
	StateUndeliverable MessageState = 5
	StateAccepted      MessageState = 6
	StateUnknown       MessageState = 7
	StateRejected      MessageState = 8
)

// statText maps a MessageState to the 7-character "stat:" keyword of the receipt text
// (SMPP v3.4 Appendix B). Unknown states fall back to UNKNOWN rather than emitting a
// malformed receipt.
func statText(s MessageState) string {
	switch s {
	case StateEnroute:
		return "ENROUTE"
	case StateDelivered:
		return "DELIVRD"
	case StateExpired:
		return "EXPIRED"
	case StateDeleted:
		return "DELETED"
	case StateUndeliverable:
		return "UNDELIV"
	case StateAccepted:
		return "ACCEPTD"
	case StateRejected:
		return "REJECTD"
	default:
		return "UNKNOWN"
	}
}

// receiptTextMaxLen bounds the "text:" field to the first 20 characters of the original
// short message, per the SMPP v3.4 delivery-receipt format.
const receiptTextMaxLen = 20

// DeliveryReceipt is the input to NewDeliveryReceipt: everything needed to build a
// deliver_sm carrying an SMSC delivery receipt for a prior submit_sm. Dates are supplied
// pre-formatted (YYMMDDhhmm) by the caller — the codec owns no clock, so the choice
// between deterministic (seeded) and wall-clock (chaos) timing stays with internal/smsc.
// The receipt flows back to the submitter, so Source/Dest are the origin submit's
// addresses swapped: the receipt's source is the number the submit was addressed to.
type DeliveryReceipt struct {
	MessageID  string       // origin message_id: the "id:" field and receipted_message_id TLV
	State      MessageState // final state: message_state TLV and derived "stat:" text
	ErrorCode  string       // "err:" field, e.g. "000"
	SubmitDate string       // "submit date:" YYMMDDhhmm
	DoneDate   string       // "done date:" YYMMDDhhmm
	Text       string       // "text:" field — first 20 chars of the original short_message

	SourceAddr    string
	SourceAddrTON uint8
	SourceAddrNPI uint8
	DestAddr      string
	DestAddrTON   uint8
	DestAddrNPI   uint8
}

// NewDeliveryReceipt builds the deliver_sm PDU for an SMSC delivery receipt: esm_class
// flags it as a receipt, the short_message carries the SMPP v3.4 Appendix B receipt
// text, and the receipted_message_id / message_state TLVs carry the same information in
// structured form (both forms per the S4 decision, for ESME parser compatibility). The
// exact receipt-text keywords must match what the gateway under test parses.
func NewDeliveryReceipt(r DeliveryReceipt) *PDU {
	return &PDU{
		CommandID: DeliverSM,
		Body: &Message{
			SourceAddr:    r.SourceAddr,
			SourceAddrTON: r.SourceAddrTON,
			SourceAddrNPI: r.SourceAddrNPI,
			DestAddr:      r.DestAddr,
			DestAddrTON:   r.DestAddrTON,
			DestAddrNPI:   r.DestAddrNPI,
			ESMClass:      ESMClassDeliveryReceipt,
			ShortMessage:  []byte(ReceiptText(r)),
			TLVs: []TLV{
				{Tag: TagReceiptedMessageID, Value: cOctetBytes(r.MessageID)},
				{Tag: TagMessageState, Value: []byte{byte(r.State)}},
			},
		},
	}
}

// ReceiptText renders the SMPP v3.4 delivery-receipt short_message. "dlvrd" (messages
// delivered) is 001 for a delivered state and 000 otherwise; "sub" (messages submitted)
// is always 001 for a single submit.
func ReceiptText(r DeliveryReceipt) string {
	dlvrd := "000"
	if r.State == StateDelivered {
		dlvrd = "001"
	}
	text := r.Text
	if len(text) > receiptTextMaxLen {
		text = text[:receiptTextMaxLen]
	}
	return fmt.Sprintf("id:%s sub:001 dlvrd:%s submit date:%s done date:%s stat:%s err:%s text:%s",
		r.MessageID, dlvrd, r.SubmitDate, r.DoneDate, statText(r.State), r.ErrorCode, text)
}

// cOctetBytes encodes a string as a null-terminated C-Octet String, the wire form of
// the receipted_message_id TLV value (SMPP v3.4 §5.3.2.28).
func cOctetBytes(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b // trailing byte is the NUL terminator (zero value)
}
