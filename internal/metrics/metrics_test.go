package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/martialanouman/go-smsc-simulator/internal/metrics"
)

// TestMetrics_CountersAndBindGauge checks the counters accumulate per virtual SMSC and
// the active-binds gauge tracks Inc/Dec symmetrically.
func TestMetrics_CountersAndBindGauge(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := metrics.New(reg)

	m.IncBind("carrier-a", "transceiver")
	m.IncBind("carrier-a", "transceiver")
	m.DecBind("carrier-a", "transceiver") // one of the two unbound: gauge should read 1
	m.IncSubmit("carrier-a")
	m.IncSubmit("carrier-a")
	m.IncOutcome("carrier-a", "success")
	m.IncOutcome("carrier-a", "error")

	expected := `
# HELP smsc_active_binds Currently bound SMPP sessions, per virtual SMSC and bind type.
# TYPE smsc_active_binds gauge
smsc_active_binds{bind_type="transceiver",virtual_smsc="carrier-a"} 1
# HELP smsc_submit_sm_received_total submit_sm PDUs accepted, per virtual SMSC.
# TYPE smsc_submit_sm_received_total counter
smsc_submit_sm_received_total{virtual_smsc="carrier-a"} 2
# HELP smsc_submit_sm_outcome_total submit_sm outcomes served, per virtual SMSC and outcome type.
# TYPE smsc_submit_sm_outcome_total counter
smsc_submit_sm_outcome_total{outcome="error",virtual_smsc="carrier-a"} 1
smsc_submit_sm_outcome_total{outcome="success",virtual_smsc="carrier-a"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"smsc_active_binds", "smsc_submit_sm_received_total", "smsc_submit_sm_outcome_total"); err != nil {
		t.Error(err)
	}
}

// TestMetrics_ActiveScenarioZeroesPrevious pins the info-gauge semantics: after a
// transition exactly one series per virtual SMSC reads 1 and the profile it replaced
// reads 0 (never left dangling at 1).
func TestMetrics_ActiveScenarioZeroesPrevious(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := metrics.New(reg)

	m.SetActiveScenario("carrier-a", "healthy")
	m.SetActiveScenario("carrier-a", "dead-carrier")

	expected := `
# HELP smsc_active_scenario Active scenario profile per virtual SMSC (1 on the current profile, 0 otherwise).
# TYPE smsc_active_scenario gauge
smsc_active_scenario{scenario="dead-carrier",virtual_smsc="carrier-a"} 1
smsc_active_scenario{scenario="healthy",virtual_smsc="carrier-a"} 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "smsc_active_scenario"); err != nil {
		t.Error(err)
	}
}

// TestMetrics_ServedLatencyObserved confirms an observation creates the histogram series
// for its (virtual_smsc, scenario) pair.
func TestMetrics_ServedLatencyObserved(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := metrics.New(reg)

	m.ObserveServedLatency("carrier-a", "slow-carrier", 3.0)

	got, err := testutil.GatherAndCount(reg, "smsc_served_latency_seconds")
	if err != nil {
		t.Fatalf("GatherAndCount: %v", err)
	}
	if got != 1 {
		t.Errorf("served-latency series count = %d, want 1 after one observation", got)
	}
}
