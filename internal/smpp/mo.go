package smpp

// NewMobileOriginated builds a deliver_sm carrying a mobile-originated (MO) message — an
// uplink SMS from a handset towards the ESME, as opposed to an SMSC delivery receipt. The
// distinction is esm_class: a receipt sets ESMClassDeliveryReceipt (0x04) and carries the
// Appendix-B receipt text plus correlation TLVs; an MO leaves esm_class 0 and puts the raw
// content straight in short_message, so the ESME routes it to its inbound-message handler
// rather than its DLR handler. The codec treats short_message as opaque bytes, so no
// encoding is imposed here.
func NewMobileOriginated(sourceAddr, destAddr, content string) *PDU {
	return &PDU{
		CommandID: DeliverSM,
		Body: &Message{
			SourceAddr:   sourceAddr,
			DestAddr:     destAddr,
			ESMClass:     0, // mobile-originated: not a delivery receipt
			ShortMessage: []byte(content),
		},
	}
}
