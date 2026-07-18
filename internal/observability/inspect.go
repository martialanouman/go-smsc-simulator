package observability

// Inspector is the read-only view the observability surface needs onto the running
// simulator. It is declared here, on the consumer side (convention §2.4): the SMPP
// engine provides the concrete implementation, and this package depends only on the
// narrow set of questions its handlers ask — never on the engine's internals.
//
// Every method is a pure read. Nothing on this interface can mutate simulator
// state, which is what keeps invariant (c) structural rather than a matter of
// handler discipline (plan §0.5). A bool return of false means "no such virtual
// SMSC", which the handler renders as 404.
type Inspector interface {
	VirtualSMSCs() []VirtualSMSCView
	VirtualSMSC(id string) (VirtualSMSCView, bool)
	ReceivedPDUs(id string, f PDUFilter) ([]RecordedPDUView, bool)
	Binds(id string) ([]BindView, bool)
	LogicalClock(id string) (uint64, bool)
}

// VirtualSMSCView is the read-only summary of one virtual SMSC. JSON keys are
// snake_case, matching the .yml vocabulary (convention §2.1).
type VirtualSMSCView struct {
	Name          string `json:"name"`
	Port          int    `json:"port"`
	ActiveProfile string `json:"active_profile"`
	BindCount     int    `json:"bind_count"`
	LogicalClock  uint64 `json:"logical_clock"`
	RecordedPDUs  int    `json:"recorded_pdus"`
}

// BindView is one active bind session. ConnectedAt is wall-clock metadata for the
// operator, never a decision input — the deterministic timing reference is
// per_bind_clock, not this timestamp (plan §1.5).
type BindView struct {
	ID          uint64 `json:"id"`
	SystemID    string `json:"system_id"`
	BindType    string `json:"bind_type"`
	ConnectedAt string `json:"connected_at"`
}

// RecordedPDUView is one received submit_sm as exposed for assertion. ShortMessage
// is a []byte, so encoding/json renders it base64 — lossless for GSM7, UCS2 or
// binary content alike (retaining content is the feature, plan §1.7).
type RecordedPDUView struct {
	Index        uint64 `json:"index"`
	MessageID    string `json:"message_id"`
	SourceAddr   string `json:"source_addr"`
	SourceTON    uint8  `json:"source_ton"`
	SourceNPI    uint8  `json:"source_npi"`
	DestAddr     string `json:"dest_addr"`
	DestTON      uint8  `json:"dest_ton"`
	DestNPI      uint8  `json:"dest_npi"`
	DataCoding   uint8  `json:"data_coding"`
	ShortMessage []byte `json:"short_message"`
	PerBindClock uint64 `json:"per_bind_clock"`
}

// PDUFilter narrows a received-pdus query. It mirrors the recorder's filter but is
// declared here so the engine package need not be imported by the surface. The
// handler fills it from the sourceAddr/destAddr/since/limit query parameters.
type PDUFilter struct {
	SourceAddr string
	DestAddr   string
	Since      uint64
	Limit      int
}
