package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server timeouts. The observability surface answers cheap, local questions, so
// generous deadlines would only hold sockets open for no benefit.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

// Server is the process-wide read-only HTTP surface: one port for the whole
// simulator, not one per virtual SMSC (plan §1.4).
//
// read-only: every route is registered with an explicit GET method pattern, so
// http.ServeMux answers 405 to any mutating verb on its own. Invariant (c) is
// therefore structural here — it holds because no mutating route can be reached,
// not because a handler remembers to check (plan §0.5).
type Server struct {
	http     *http.Server
	listener net.Listener
	logger   *slog.Logger
	insp     Inspector
}

// NewServer binds the observability port and prepares the read-only surface.
//
// The listener is opened here rather than in Serve so that a port conflict is
// reported at boot, next to the other fail-fast errors, and so that callers can
// pass port 0 and read back the OS-assigned address via Addr. Tests rely on
// that: they bind ephemeral ports to stay parallel-safe.
//
// insp is the read-only view onto the running simulator; the /v1 inspection routes
// are registered only when it is non-nil, so a black-box or pre-engine boot still
// serves /health alone. insp is never allowed to mutate state — see Inspector.
//
// STUB S6: /metrics lands at S6. See plan §10.
func NewServer(port int, logger *slog.Logger, insp Inspector) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen on observability port %d: %w", port, err)
	}

	s := &Server{listener: ln, logger: logger, insp: insp}

	mux := http.NewServeMux()
	// The "GET " prefix is load-bearing: it is what makes POST/PUT/PATCH/DELETE
	// return 405 without a single line of defensive code. Every route added here
	// must keep it, or the read-only invariant (c) breaks (plan §0.5).
	mux.HandleFunc("GET /health", s.handleHealth)

	if insp != nil {
		mux.HandleFunc("GET /v1/virtual-smscs", s.handleVirtualSMSCs)
		mux.HandleFunc("GET /v1/virtual-smscs/{id}", s.handleVirtualSMSC)
		mux.HandleFunc("GET /v1/virtual-smscs/{id}/received-pdus", s.handleReceivedPDUs)
		mux.HandleFunc("GET /v1/virtual-smscs/{id}/binds", s.handleBinds)
		mux.HandleFunc("GET /v1/virtual-smscs/{id}/logical-clock", s.handleLogicalClock)
	}

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return s, nil
}

// Addr reports the address the server is bound to, with the real port resolved
// when port 0 was requested.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Serve runs the surface until Shutdown is called. It returns nil on a clean
// shutdown, so callers can treat any non-nil return as a genuine failure.
func (s *Server) Serve() error {
	s.logger.Info("observability surface listening", slog.String("addr", s.Addr().String()))

	if err := s.http.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve observability surface: %w", err)
	}
	return nil
}

// Shutdown stops the surface gracefully, letting in-flight reads finish within
// ctx's deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown observability surface: %w", err)
	}
	return nil
}

// handleHealth reports process liveness.
//
// read-only: it never touches simulator state. It answers "is this process up?",
// deliberately not "are the virtual SMSCs healthy?" — the simulator is a test
// peer whose whole job includes pretending to be unhealthy, so folding scenario
// state into /health would make a dead-carrier run look like a broken process.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		s.logger.Warn("write health response", slog.Any("error", err))
	}
}
