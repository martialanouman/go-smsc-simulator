package smpp_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// TestCodec_RoundTrip is the codec's core contract (plan §6 acceptance): for every
// PDU in the corpus, Decode(ReadPDU(Encode(pdu))) must reproduce the original
// exactly. Framing goes through ReadPDU so command_length handling is exercised too.
func TestCodec_RoundTrip(t *testing.T) {
	t.Parallel()

	udh := []byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01} // 6-byte UDH prefix
	bigPayload := bytes.Repeat([]byte("x"), 300)      // > 254: lives in a TLV

	cases := []struct {
		name string
		pdu  smpp.PDU
	}{
		{"bind_transmitter", smpp.PDU{
			CommandID: smpp.BindTransmitter, SequenceNumber: 1,
			Body: &smpp.Bind{SystemID: "smppclient1", Password: "secret", SystemType: "", InterfaceVersion: 0x34, AddrTON: 1, AddrNPI: 1, AddressRange: "^33.*"},
		}},
		{"bind_receiver", smpp.PDU{
			CommandID: smpp.BindReceiver, SequenceNumber: 2,
			Body: &smpp.Bind{SystemID: "rx", Password: "pw", InterfaceVersion: 0x34},
		}},
		{"bind_transceiver", smpp.PDU{
			CommandID: smpp.BindTransceiver, SequenceNumber: 3,
			Body: &smpp.Bind{SystemID: "trx", Password: "pw", SystemType: "smpp", InterfaceVersion: 0x34, AddrTON: 5, AddrNPI: 0},
		}},
		{"bind_transceiver_resp", smpp.PDU{
			CommandID: smpp.BindTransceiverResp, CommandStatus: smpp.StatusROK, SequenceNumber: 3,
			Body: &smpp.BindResp{SystemID: "carrier-healthy"},
		}},
		{"bind_transceiver_resp_with_tlv", smpp.PDU{
			CommandID: smpp.BindTransceiverResp, SequenceNumber: 4,
			Body: &smpp.BindResp{SystemID: "carrier", TLVs: []smpp.TLV{{Tag: 0x0210, Value: []byte{0x34}}}},
		}},
		{"submit_sm", smpp.PDU{
			CommandID: smpp.SubmitSM, SequenceNumber: 10,
			Body: &smpp.Message{
				SourceAddrTON: 1, SourceAddrNPI: 1, SourceAddr: "33600000000",
				DestAddrTON: 1, DestAddrNPI: 1, DestAddr: "33611111111",
				DataCoding: 0, ShortMessage: []byte("hello world"),
			},
		}},
		{"submit_sm_with_udh_and_tlv", smpp.PDU{
			CommandID: smpp.SubmitSM, SequenceNumber: 11,
			Body: &smpp.Message{
				ServiceType: "CMT", SourceAddr: "1234", DestAddr: "5678",
				ESMClass:     0x40, // UDHI set
				ShortMessage: udh,
				TLVs:         []smpp.TLV{{Tag: 0x0424, Value: []byte("payload")}, {Tag: 0x0201, Value: []byte{0x00, 0x01}}},
			},
		}},
		{"submit_sm_large_payload_in_tlv", smpp.PDU{
			CommandID: smpp.SubmitSM, SequenceNumber: 12,
			Body: &smpp.Message{
				SourceAddr: "1", DestAddr: "2",
				ShortMessage: []byte{}, // empty: content moved to message_payload
				TLVs:         []smpp.TLV{{Tag: smpp.TagMessagePayload, Value: bigPayload}},
			},
		}},
		{"submit_sm_resp", smpp.PDU{
			CommandID: smpp.SubmitSMResp, CommandStatus: smpp.StatusROK, SequenceNumber: 10,
			Body: &smpp.SubmitResp{MessageID: "7-0001"},
		}},
		{"deliver_sm_dlr", smpp.PDU{
			CommandID: smpp.DeliverSM, SequenceNumber: 20,
			Body: &smpp.Message{
				SourceAddr: "5678", DestAddr: "1234", ESMClass: 0x04, // delivery receipt
				ShortMessage: []byte("id:7-0001 stat:DELIVRD err:000"),
			},
		}},
		{"enquire_link", smpp.PDU{CommandID: smpp.EnquireLink, SequenceNumber: 99}},
		{"enquire_link_resp", smpp.PDU{CommandID: smpp.EnquireLinkResp, CommandStatus: smpp.StatusROK, SequenceNumber: 99}},
		{"unbind", smpp.PDU{CommandID: smpp.Unbind, SequenceNumber: 100}},
		{"unbind_resp", smpp.PDU{CommandID: smpp.UnbindResp, SequenceNumber: 100}},
		{"generic_nack", smpp.PDU{CommandID: smpp.GenericNack, CommandStatus: smpp.StatusInvCmdID, SequenceNumber: 5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := smpp.Encode(&tc.pdu)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			frame, err := smpp.ReadPDU(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("ReadPDU: %v", err)
			}
			if !bytes.Equal(frame, encoded) {
				t.Fatalf("ReadPDU returned %d bytes, Encode produced %d", len(frame), len(encoded))
			}

			got, err := smpp.Decode(frame)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got, &tc.pdu) {
				t.Fatalf("round-trip mismatch\n got: %#v\nwant: %#v", got, &tc.pdu)
			}
		})
	}
}

// TestReadPDU_Framing checks that ReadPDU reports the right error for each way a
// stream can be malformed, and never blocks or panics.
func TestReadPDU_Framing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []byte
		wantErr error
	}{
		{"clean eof between pdus", nil, io.EOF},
		{"command_length below header", []byte{0, 0, 0, 8, 0, 0, 0, 0}, smpp.ErrBadCommandLength},
		{"command_length above max", []byte{0xFF, 0xFF, 0xFF, 0xFF}, smpp.ErrBadCommandLength},
		{"truncated body", []byte{0, 0, 0, 32, 0, 0, 0, 4}, io.ErrUnexpectedEOF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := smpp.ReadPDU(bytes.NewReader(tc.input))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ReadPDU error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestDecode_Errors checks that a well-framed but semantically malformed PDU
// returns an error instead of panicking — the contract the fuzz harness relies on.
func TestDecode_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		frame   []byte
		wantErr error
	}{
		{"shorter than header", []byte{0, 0, 0, 4}, smpp.ErrShortPDU},
		{
			name:    "unknown command_id",
			frame:   []byte{0, 0, 0, 16, 0, 0, 0, 0x7F, 0, 0, 0, 0, 0, 0, 0, 1},
			wantErr: smpp.ErrUnknownCommand,
		},
		{
			name:    "bind body unterminated system_id",
			frame:   append([]byte{0, 0, 0, 20, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{'a', 'b', 'c', 'd'}...),
			wantErr: smpp.ErrUnterminated,
		},
		{
			name:    "submit_sm truncated before short_message",
			frame:   []byte{0, 0, 0, 17, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 1, 0},
			wantErr: smpp.ErrTruncated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := smpp.Decode(tc.frame)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Decode error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
