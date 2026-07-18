package observability

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// handleVirtualSMSCs lists every virtual SMSC and its active profile.
func (s *Server) handleVirtualSMSCs(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, s.insp.VirtualSMSCs())
}

// handleVirtualSMSC returns one virtual SMSC's summary, 404 if the id is unknown.
func (s *Server) handleVirtualSMSC(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, ok := s.insp.VirtualSMSC(id)
	if !ok {
		s.notFound(w, id)
		return
	}
	s.writeJSON(w, view)
}

// handleReceivedPDUs returns the recorded submit_sm PDUs, narrowed by the
// sourceAddr / destAddr / since / limit query parameters. Unparseable numeric
// params are treated as absent rather than rejected — inspection is best-effort.
func (s *Server) handleReceivedPDUs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	f := PDUFilter{
		SourceAddr: q.Get("sourceAddr"),
		DestAddr:   q.Get("destAddr"),
		Since:      parseUint(q.Get("since")),
		Limit:      pageLimit(q.Get("limit")),
	}
	pdus, ok := s.insp.ReceivedPDUs(id, f)
	if !ok {
		s.notFound(w, id)
		return
	}
	s.writeJSON(w, pdus)
}

// handleBinds returns the active bind sessions of one virtual SMSC.
func (s *Server) handleBinds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	binds, ok := s.insp.Binds(id)
	if !ok {
		s.notFound(w, id)
		return
	}
	s.writeJSON(w, binds)
}

// handleLogicalClock returns the current global submit_sm count — the assertion
// observable of plan §1.5, never a scheduling reference.
func (s *Server) handleLogicalClock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	clock, ok := s.insp.LogicalClock(id)
	if !ok {
		s.notFound(w, id)
		return
	}
	s.writeJSON(w, map[string]uint64{"logical_clock": clock})
}

// writeJSON encodes v as the 200 response body. A late encode failure (client gone)
// is logged, not surfaced — the status line is already committed.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("write inspection response", slog.Any("error", err))
	}
}

func (s *Server) notFound(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": "unknown virtual smsc: " + id}); err != nil {
		s.logger.Warn("write not-found response", slog.Any("error", err))
	}
}

// maxPDUPage bounds a single received-pdus response so a paging request can never
// serialize an entire large buffer at once. It is also the default when limit is
// absent, invalid or non-positive, so a malformed limit yields a bounded page rather
// than the surprise of the whole buffer.
const maxPDUPage = 1000

// pageLimit resolves the limit query parameter to a positive, capped page size.
func pageLimit(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 || v > maxPDUPage {
		return maxPDUPage
	}
	return v
}

// parseUint returns the parsed value or 0 for empty/invalid input (absent filter).
func parseUint(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
