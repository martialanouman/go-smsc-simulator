// Package metrics exposes Prometheus instrumentation for the virtual SMSCs, one series
// set per instance. Every metric is labelled only from the bounded set
// {virtual_smsc, bind_type, outcome, scenario, reason, initiator, kind} — each a closed
// enumeration, never a MSISDN, message_id, system_id, address or content,
// whose unbounded cardinality would be both a memory leak and a data leak (CLAUDE.md).
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds the Prometheus collectors for the SMPP engine, registered on one
// registry. Its methods are the sink the engine calls; they are safe for concurrent use
// (the underlying collectors are, and the active-scenario bookkeeping is mutex-guarded).
type Metrics struct {
	activeBinds    *prometheus.GaugeVec
	submitReceived *prometheus.CounterVec
	submitOutcome  *prometheus.CounterVec
	activeScenario *prometheus.GaugeVec
	servedLatency  *prometheus.HistogramVec
	sessionClosed  *prometheus.CounterVec
	outboundDepth  *prometheus.GaugeVec
	outboundDrop   *prometheus.CounterVec

	// mu guards scenario, the per-virtual-SMSC record of which profile currently reads 1
	// on activeScenario, so SetActiveScenario can zero the profile it replaces.
	mu       sync.Mutex
	scenario map[string]string
}

// New builds and registers the collectors on reg.
func New(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		activeBinds: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "smsc_active_binds",
			Help: "Currently bound SMPP sessions, per virtual SMSC and bind type.",
		}, []string{"virtual_smsc", "bind_type"}),
		submitReceived: f.NewCounterVec(prometheus.CounterOpts{
			Name: "smsc_submit_sm_received_total",
			Help: "submit_sm PDUs accepted, per virtual SMSC.",
		}, []string{"virtual_smsc"}),
		submitOutcome: f.NewCounterVec(prometheus.CounterOpts{
			Name: "smsc_submit_sm_outcome_total",
			Help: "submit_sm outcomes served, per virtual SMSC and outcome type.",
		}, []string{"virtual_smsc", "outcome"}),
		activeScenario: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "smsc_active_scenario",
			Help: "Active scenario profile per virtual SMSC (1 on the current profile, 0 otherwise).",
		}, []string{"virtual_smsc", "scenario"}),
		servedLatency: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "smsc_served_latency_seconds",
			Help:    "Served submit_sm latency in seconds, per virtual SMSC and scenario.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // ~1ms .. ~16s, covers slow-carrier's 2-4s
		}, []string{"virtual_smsc", "scenario"}),
		sessionClosed: f.NewCounterVec(prometheus.CounterOpts{
			Name: "smsc_session_closed_total",
			Help: "SMPP sessions closed, per virtual SMSC, bind type, closed reason and initiator.",
		}, []string{"virtual_smsc", "bind_type", "reason", "initiator"}),
		outboundDepth: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "smsc_outbound_queue_depth",
			Help: "Encoded PDUs queued for writing, summed over the sessions of a virtual SMSC.",
		}, []string{"virtual_smsc"}),
		outboundDrop: f.NewCounterVec(prometheus.CounterOpts{
			Name: "smsc_outbound_dropped_total",
			Help: "Outbound PDUs discarded because the session writer had failed, per virtual SMSC and kind (resp, dlr, mo).",
		}, []string{"virtual_smsc", "kind"}),
		scenario: make(map[string]string),
	}
}

// IncBind records a successful bind on a virtual SMSC.
func (m *Metrics) IncBind(virtualSMSC, bindType string) {
	m.activeBinds.WithLabelValues(virtualSMSC, bindType).Inc()
}

// DecBind records a bind teardown on a virtual SMSC.
func (m *Metrics) DecBind(virtualSMSC, bindType string) {
	m.activeBinds.WithLabelValues(virtualSMSC, bindType).Dec()
}

// IncSubmit records an accepted submit_sm on a virtual SMSC.
func (m *Metrics) IncSubmit(virtualSMSC string) {
	m.submitReceived.WithLabelValues(virtualSMSC).Inc()
}

// IncOutcome records a served submit_sm outcome (success/error/timeout/disconnect).
func (m *Metrics) IncOutcome(virtualSMSC, outcome string) {
	m.submitOutcome.WithLabelValues(virtualSMSC, outcome).Inc()
}

// ObserveServedLatency records the served latency of a submit_sm, in seconds.
func (m *Metrics) ObserveServedLatency(virtualSMSC, scenario string, seconds float64) {
	m.servedLatency.WithLabelValues(virtualSMSC, scenario).Observe(seconds)
}

// SetActiveScenario marks scenario as the active profile for virtualSMSC, zeroing the
// profile it replaces so exactly one series per virtual SMSC reads 1. The scenario label
// set is bounded by the profiles a virtual SMSC can run (its initial profile plus its
// transition targets), so cardinality stays bounded.
func (m *Metrics) SetActiveScenario(virtualSMSC, scenario string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.scenario[virtualSMSC]; ok && prev != scenario {
		m.activeScenario.WithLabelValues(virtualSMSC, prev).Set(0)
	}
	m.activeScenario.WithLabelValues(virtualSMSC, scenario).Set(1)
	m.scenario[virtualSMSC] = scenario
}

// IncSessionClosed records a session close with its closed-set reason and initiator.
func (m *Metrics) IncSessionClosed(virtualSMSC, bindType, reason, initiator string) {
	m.sessionClosed.WithLabelValues(virtualSMSC, bindType, reason, initiator).Inc()
}

// AddOutboundDepth moves the aggregate outbound queue depth of a virtual SMSC by delta.
func (m *Metrics) AddOutboundDepth(virtualSMSC string, delta float64) {
	m.outboundDepth.WithLabelValues(virtualSMSC).Add(delta)
}

// IncOutboundDropped records an outbound PDU discarded after its session writer failed.
func (m *Metrics) IncOutboundDropped(virtualSMSC, kind string) {
	m.outboundDrop.WithLabelValues(virtualSMSC, kind).Inc()
}
