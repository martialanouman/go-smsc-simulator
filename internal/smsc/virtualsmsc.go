package smsc

import (
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/recorder"
	"github.com/martialanouman/go-smsc-simulator/internal/scenario"
)

// virtualSMSC is one SMPP endpoint: its own listener, credentials, recorder,
// scenario engine and clocks. A process hosts several of these independently
// (multi-instance isolation with per-instance recover is deferred to S6, plan §10);
// at S2 they simply coexist, each on its own port.
type virtualSMSC struct {
	cfg      config.VirtualSMSCConfig
	listener net.Listener
	recorder *recorder.Recorder
	scenario *scenario.Engine
	binds    *bindRegistry
	logger   *slog.Logger

	// logicalClock is the per-virtual-SMSC submit_sm counter — a global assertion
	// observable only (GET /logical-clock), never a scheduling reference; its order
	// across concurrent binds is not reproducible (plan §1.5).
	logicalClock atomic.Uint64
	// bindSeq assigns each accepted session a monotonic ordinal, used both as the
	// bind id in /binds and as the high part of the deterministic message_id.
	bindSeq atomic.Uint64
	// dlrDropped counts delivery receipts that could not be emitted for a mapping
	// reason (a transmitter-only origin bind cannot carry the return deliver_sm) — never
	// dropped silently: each is also logged. Surfaced read-only via Engine.DLRsDropped.
	dlrDropped atomic.Uint64
}

func newVirtualSMSC(cfg config.VirtualSMSCConfig, ln net.Listener, logger *slog.Logger) *virtualSMSC {
	return &virtualSMSC{
		cfg:      cfg,
		listener: ln,
		recorder: recorder.New(cfg.PDUBufferSize),
		scenario: scenario.New(cfg.Scenario, cfg.ThroughputLimitPerSec),
		binds:    newBindRegistry(),
		logger:   logger.With(slog.String("virtual_smsc", cfg.Name)),
	}
}

// view assembles the read-only summary of this virtual SMSC.
func (v *virtualSMSC) view() observability.VirtualSMSCView {
	return observability.VirtualSMSCView{
		Name:          v.cfg.Name,
		Port:          v.cfg.Port,
		ActiveProfile: string(v.cfg.Scenario.Profile),
		BindCount:     v.binds.count(),
		LogicalClock:  v.logicalClock.Load(),
		RecordedPDUs:  v.recorder.Len(),
	}
}
