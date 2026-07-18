// Package smpp is the simulator's own SMPP v3.4 codec, used on both sides: the
// server engine decodes client requests and encodes responses, and the in-process
// test client (internal/smpptest) does the reverse. One codec, exercised from both
// ends, so a client↔server round-trip double-validates it (plan §1.1, §13).
//
// The package is deliberately dependency-free — no external SMPP library — and holds
// no config or scenario knowledge: it turns bytes into PDUs and back, nothing more.
// Mapping a scenario outcome onto a CommandStatus belongs to the caller (internal/smsc).
package smpp

// PDU is one decoded SMPP protocol data unit: the three addressable header fields
// plus a typed body. command_length is never stored — it is a function of the
// encoded form, computed on Encode and consumed on Decode, so it can never drift
// out of sync with the actual bytes.
//
// Body is nil for header-only PDUs (enquire_link, unbind, their responses, and
// generic_nack). For every other command it is one of the concrete body types in
// this file; the Body interface is sealed (its only method is unexported) so the
// set of PDU shapes is closed and exhaustively known to Decode.
type PDU struct {
	CommandID      CommandID
	CommandStatus  CommandStatus
	SequenceNumber uint32
	Body           Body
}

// Body is the sealed set of SMPP PDU bodies this codec models. The unexported
// marshal method is what seals it: only types in this package can satisfy Body,
// so Decode's command dispatch is total.
type Body interface {
	marshal(w *writer)
}

// TLV is a tag-length-value optional parameter. The value is kept opaque so any
// TLV — including message_payload (TagMessagePayload) carrying a body larger than
// the 254-octet short_message limit — round-trips byte-for-byte without the codec
// needing to understand it.
type TLV struct {
	Tag   uint16
	Value []byte
}

// TagMessagePayload is the optional-parameter tag whose value carries the message
// content when it exceeds the 254-octet short_message field (SMPP v3.4 §5.3.2.32).
const TagMessagePayload uint16 = 0x0424

// Bind is the body of bind_transmitter / bind_receiver / bind_transceiver: the
// credentials and address parameters a client presents. The CommandID on the
// enclosing PDU says which of the three it is.
type Bind struct {
	SystemID         string
	Password         string
	SystemType       string
	InterfaceVersion uint8
	AddrTON          uint8
	AddrNPI          uint8
	AddressRange     string
}

func (b *Bind) marshal(w *writer) {
	w.cOctetString(b.SystemID)
	w.cOctetString(b.Password)
	w.cOctetString(b.SystemType)
	w.byte(b.InterfaceVersion)
	w.byte(b.AddrTON)
	w.byte(b.AddrNPI)
	w.cOctetString(b.AddressRange)
}

// BindResp is the body of a bind_*_resp: the SMSC's system_id and any optional
// TLVs (e.g. sc_interface_version). Kept distinct from SubmitResp because the
// leading field means system_id here, message_id there.
type BindResp struct {
	SystemID string
	TLVs     []TLV
}

func (b *BindResp) marshal(w *writer) {
	w.cOctetString(b.SystemID)
	w.tlvs(b.TLVs)
}

// Message is the body shared by submit_sm and deliver_sm — structurally identical
// in SMPP v3.4. ShortMessage is stored opaque, so a UDH prefix (signalled by the
// UDHI bit in ESMClass) round-trips without the codec parsing it. TLVs preserves
// optional parameters in order.
type Message struct {
	ServiceType          string
	SourceAddrTON        uint8
	SourceAddrNPI        uint8
	SourceAddr           string
	DestAddrTON          uint8
	DestAddrNPI          uint8
	DestAddr             string
	ESMClass             uint8
	ProtocolID           uint8
	PriorityFlag         uint8
	ScheduleDeliveryTime string
	ValidityPeriod       string
	RegisteredDelivery   uint8
	ReplaceIfPresent     uint8
	DataCoding           uint8
	SMDefaultMsgID       uint8
	ShortMessage         []byte
	TLVs                 []TLV
}

func (m *Message) marshal(w *writer) {
	w.cOctetString(m.ServiceType)
	w.byte(m.SourceAddrTON)
	w.byte(m.SourceAddrNPI)
	w.cOctetString(m.SourceAddr)
	w.byte(m.DestAddrTON)
	w.byte(m.DestAddrNPI)
	w.cOctetString(m.DestAddr)
	w.byte(m.ESMClass)
	w.byte(m.ProtocolID)
	w.byte(m.PriorityFlag)
	w.cOctetString(m.ScheduleDeliveryTime)
	w.cOctetString(m.ValidityPeriod)
	w.byte(m.RegisteredDelivery)
	w.byte(m.ReplaceIfPresent)
	w.byte(m.DataCoding)
	w.byte(m.SMDefaultMsgID)
	// sm_length is a single octet: content longer than the field can hold must travel
	// in the message_payload TLV, so clamp defensively rather than wrap the length.
	smLen := len(m.ShortMessage)
	if smLen > maxShortMessageLen {
		smLen = maxShortMessageLen
	}
	w.byte(uint8(smLen))
	w.octets(m.ShortMessage[:smLen])
	w.tlvs(m.TLVs)
}

// maxShortMessageLen is the largest short_message the single-octet sm_length can
// address (SMPP v3.4 §5.2.21). Larger payloads use message_payload (TagMessagePayload).
const maxShortMessageLen = 254

// SubmitResp is the body of submit_sm_resp / deliver_sm_resp: the assigned
// message_id (empty on a deliver_sm_resp). The simulator mints this id
// deterministically so a later DLR can correlate back to it (plan §6, §8).
type SubmitResp struct {
	MessageID string
}

func (s *SubmitResp) marshal(w *writer) {
	w.cOctetString(s.MessageID)
}
