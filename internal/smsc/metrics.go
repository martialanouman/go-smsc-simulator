package smsc

import "github.com/martialanouman/go-smsc-simulator/internal/scenario"

// metricsSink is smsc's consumer-side view of the Prometheus instrumentation:
// internal/metrics.Metrics satisfies it structurally. Defining the interface here rather
// than importing the collector keeps smsc dependent on the abstraction it needs, not on
// the implementation (CLAUDE.md: interfaces defined consumer-side). The no-op default
// (noopMetrics) keeps the hot path free of nil checks and lets tests run without a registry.
type metricsSink interface {
	IncBind(virtualSMSC, bindType string)
	DecBind(virtualSMSC, bindType string)
	IncSubmit(virtualSMSC string)
	IncOutcome(virtualSMSC, outcome string)
	ObserveServedLatency(virtualSMSC, scenario string, seconds float64)
	SetActiveScenario(virtualSMSC, scenario string)
}

// noopMetrics is the default sink when none is supplied (tests, black-box boots). Every
// method is a no-op, so instrumentation call sites never guard against a nil sink.
type noopMetrics struct{}

func (noopMetrics) IncBind(string, string)                       {}
func (noopMetrics) DecBind(string, string)                       {}
func (noopMetrics) IncSubmit(string)                             {}
func (noopMetrics) IncOutcome(string, string)                    {}
func (noopMetrics) ObserveServedLatency(string, string, float64) {}
func (noopMetrics) SetActiveScenario(string, string)             {}

// outcomeLabel maps a scenario outcome to its bounded metric label. There is no
// Outcome.String() to reuse, and the label set must stay closed for the cardinality
// guard, so the mapping is explicit.
func outcomeLabel(o scenario.Outcome) string {
	switch o {
	case scenario.OutcomeSuccess:
		return "success"
	case scenario.OutcomeError:
		return "error"
	case scenario.OutcomeTimeout:
		return "timeout"
	case scenario.OutcomeDisconnect:
		return "disconnect"
	default:
		return "unknown"
	}
}
