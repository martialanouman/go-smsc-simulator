// Package observability provides the simulator's diagnostic plumbing: the
// structured logger, the Prometheus registry, and the read-only HTTP surface.
//
// Everything here is strictly read-only with respect to simulator state. The
// surface answers questions ("which binds are up?", "what was submitted?"); it
// never changes an answer. A mutating endpoint is a bug, not a feature
// (invariant c, plan §0.5).
package observability

import (
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// NewLogger builds the process logger: structured slog over JSON, so CI can
// parse runs without scraping prose.
//
// Note the deliberate asymmetry with the PDU recorder (plan §1.7): the recorder
// retains submit_sm content because inspecting it is the product. Logs do not
// echo that content at info level — it is read via GET /received-pdus instead.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// NewRegistry builds a dedicated Prometheus registry.
//
// It is deliberately not the global default registry: a global would smuggle in
// Go runtime and process collectors that nobody asked for, and would make two
// simulators in one test binary collide. Collectors are registered by the
// components that own them.
//
// STUB S6: the registry exists so wiring is settled, but no /metrics endpoint
// serves it yet, and no collector is registered. Both land at S6. See plan §10.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}
