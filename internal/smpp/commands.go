package smpp

import "strconv"

// CommandID is the SMPP command_id header field: which operation a PDU carries.
// Response command ids are the request id with the high bit set (SMPP v3.4 §5.1.2.1).
type CommandID uint32

// Command ids handled by the simulator (SMPP v3.4). The set is intentionally the
// subset the server engine needs — request ids it decodes, response ids it
// encodes — plus generic_nack for rejecting the unparseable.
const (
	BindReceiver        CommandID = 0x00000001
	BindTransmitter     CommandID = 0x00000002
	SubmitSM            CommandID = 0x00000004
	DeliverSM           CommandID = 0x00000005
	Unbind              CommandID = 0x00000006
	BindTransceiver     CommandID = 0x00000009
	EnquireLink         CommandID = 0x00000015
	GenericNack         CommandID = 0x80000000
	BindReceiverResp    CommandID = 0x80000001
	BindTransmitterResp CommandID = 0x80000002
	SubmitSMResp        CommandID = 0x80000004
	DeliverSMResp       CommandID = 0x80000005
	UnbindResp          CommandID = 0x80000006
	BindTransceiverResp CommandID = 0x80000009
	EnquireLinkResp     CommandID = 0x80000015
)

// String renders the command id by name for logs and test failures, falling back
// to hex for an unmodelled id.
func (c CommandID) String() string {
	switch c {
	case BindReceiver:
		return "bind_receiver"
	case BindTransmitter:
		return "bind_transmitter"
	case SubmitSM:
		return "submit_sm"
	case DeliverSM:
		return "deliver_sm"
	case Unbind:
		return "unbind"
	case BindTransceiver:
		return "bind_transceiver"
	case EnquireLink:
		return "enquire_link"
	case GenericNack:
		return "generic_nack"
	case BindReceiverResp:
		return "bind_receiver_resp"
	case BindTransmitterResp:
		return "bind_transmitter_resp"
	case SubmitSMResp:
		return "submit_sm_resp"
	case DeliverSMResp:
		return "deliver_sm_resp"
	case UnbindResp:
		return "unbind_resp"
	case BindTransceiverResp:
		return "bind_transceiver_resp"
	case EnquireLinkResp:
		return "enquire_link_resp"
	default:
		return "command(0x" + strconv.FormatUint(uint64(c), 16) + ")"
	}
}

// CommandStatus is the SMPP command_status header field: ESME_ROK on success, an
// error code otherwise. The engine translates a scenario outcome into one of these;
// the codec only carries the number.
type CommandStatus uint32

// Command statuses the simulator emits (SMPP v3.4 §5.1.3). Values match the wire
// numbers; the string names mirror config.SMPPErrorCode so a status seen on the
// wire reads the same as the one written in a .yml.
const (
	StatusROK        CommandStatus = 0x00000000
	StatusInvMsgLen  CommandStatus = 0x00000001
	StatusInvCmdLen  CommandStatus = 0x00000002
	StatusInvCmdID   CommandStatus = 0x00000003
	StatusInvBndSts  CommandStatus = 0x00000004
	StatusSysErr     CommandStatus = 0x00000008
	StatusInvSrcAdr  CommandStatus = 0x0000000A
	StatusInvDstAdr  CommandStatus = 0x0000000B
	StatusBindFail   CommandStatus = 0x0000000D
	StatusMsgQFul    CommandStatus = 0x00000014
	StatusSubmitFail CommandStatus = 0x00000045
	StatusThrottled  CommandStatus = 0x00000058
)
