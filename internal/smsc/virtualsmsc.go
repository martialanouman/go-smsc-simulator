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
	// engines holds one immutable Engine per profile this SMSC can run: the initial profile
	// plus every scheduled_transitions target. A session swaps its current engine pointer to
	// engines[toProfile] when a transition fires — the only path that moves active_scenario
	// (spec §6.1). Built once at boot, never mutated, so concurrent reads need no lock.
	engines map[config.Profile]*scenario.Engine
	binds   *bindRegistry
	logger  *slog.Logger

	// activeProfile is the read-only observable of the currently active profile. It is a
	// best-effort cross-bind view (like logicalClock, its order across concurrent binds on
	// different clocks is not reproducible): whichever bind last applied a transition wins.
	activeProfile atomic.Value // string

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
	initial := scenario.New(cfg.Scenario, cfg.ThroughputLimitPerSec)
	v := &virtualSMSC{
		cfg:      cfg,
		listener: ln,
		recorder: recorder.New(cfg.PDUBufferSize),
		scenario: initial,
		engines:  buildEngines(cfg, initial),
		binds:    newBindRegistry(),
		logger:   logger.With(slog.String("virtual_smsc", cfg.Name)),
	}
	v.activeProfile.Store(string(cfg.Scenario.Profile))
	return v
}

// buildEngines assembles the engine per profile a session can switch to: the initial
// profile (the fully configured engine, so a transition BACK restores its params/DLR) plus
// one bare engine per distinct scheduled_transitions target. Transition targets name a bare
// profile with no params — enough for the parameterless reference profiles (healthy,
// dead-carrier, slow-carrier); a target needing tuned knobs is out of scope (spec §6.1).
func buildEngines(cfg config.VirtualSMSCConfig, initial *scenario.Engine) map[config.Profile]*scenario.Engine {
	engines := map[config.Profile]*scenario.Engine{cfg.Scenario.Profile: initial}
	for _, tr := range cfg.ScheduledTransitions {
		if _, ok := engines[tr.ToProfile]; ok {
			continue
		}
		engines[tr.ToProfile] = scenario.New(config.ScenarioConfig{Profile: tr.ToProfile}, cfg.ThroughputLimitPerSec)
	}
	return engines
}

// setActiveProfile records the profile a bind just transitioned to, for the read-only
// observable. Called from a session's read goroutine when a transition fires.
func (v *virtualSMSC) setActiveProfile(p config.Profile) {
	v.activeProfile.Store(string(p))
}

// view assembles the read-only summary of this virtual SMSC.
func (v *virtualSMSC) view() observability.VirtualSMSCView {
	return observability.VirtualSMSCView{
		Name:          v.cfg.Name,
		Port:          v.cfg.Port,
		ActiveProfile: v.activeProfile.Load().(string),
		BindCount:     v.binds.count(),
		LogicalClock:  v.logicalClock.Load(),
		RecordedPDUs:  v.recorder.Len(),
	}
}
