// Package smsc is the SMPP Server Engine: it hosts the virtual SMSCs described by
// the config, accepts client binds, and answers submit_sm according to the active
// scenario. It is the live core the read-only surface observes (plan §6).
//
// Concurrency model (CLAUDE.md rule of thumb): one reader and one writer goroutine
// per connection, with the session's SMPP state owned by a single goroutine — no
// lock on the window. The only shared, mutex-guarded state is the per-virtual-SMSC
// recorder and bind registry, which the HTTP surface reads.
package smsc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/recorder"
)

// Engine hosts every virtual SMSC in one process and implements
// observability.Inspector so the read-only surface can be built against it.
type Engine struct {
	smscs  []*virtualSMSC
	byName map[string]*virtualSMSC
	logger *slog.Logger

	// quit is closed by Shutdown to signal every session goroutine to stop; wg
	// tracks accept loops and live sessions so Shutdown can wait for a clean drain.
	quit     chan struct{}
	quitOnce sync.Once
	wg       sync.WaitGroup

	// mu guards serving. It also orders Serve's initial wg.Add calls before any
	// Shutdown wg.Wait: Serve registers the accept loops while holding mu, and
	// Shutdown reads serving under mu, so a Wait can never start from a zero counter
	// concurrently with those Adds (a WaitGroup misuse the race detector flags).
	mu      sync.Mutex
	serving bool
}

// New binds a listener for every virtual SMSC up front, so a port conflict fails at
// boot — next to the config's other fail-fast errors — rather than after some
// endpoints are already live (mirrors observability.NewServer, plan §6). On any
// bind failure it closes the listeners already opened and returns the error, so no
// half-open engine leaks.
func New(cfgs []config.VirtualSMSCConfig, logger *slog.Logger) (*Engine, error) {
	e := &Engine{
		byName: make(map[string]*virtualSMSC, len(cfgs)),
		logger: logger,
		quit:   make(chan struct{}),
	}

	for _, cfg := range cfgs {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
		if err != nil {
			e.closeListeners()
			return nil, fmt.Errorf("listen on smpp port %d for %q: %w", cfg.Port, cfg.Name, err)
		}
		v := newVirtualSMSC(cfg, ln, logger)
		e.smscs = append(e.smscs, v)
		e.byName[cfg.Name] = v
	}
	return e, nil
}

// Serve runs the accept loops until Shutdown is called, then drains and returns nil.
// Like http.Server.Serve, a non-nil return is always a genuine failure. It blocks on
// quit rather than on the WaitGroup directly, so a process hosting zero virtual SMSCs
// (a black-box config) still waits for shutdown instead of returning immediately.
func (e *Engine) Serve() error {
	e.mu.Lock()
	e.serving = true
	for _, v := range e.smscs {
		e.wg.Add(1)
		go func(v *virtualSMSC) {
			defer e.wg.Done()
			e.acceptLoop(v)
		}(v)
		e.logger.Info("virtual smsc listening",
			slog.String("virtual_smsc", v.cfg.Name), slog.String("addr", v.listener.Addr().String()))
	}
	e.mu.Unlock()

	<-e.quit
	e.wg.Wait()
	return nil
}

// Shutdown stops accepting, signals live sessions to unwind, and waits for them to
// drain within ctx. Closing the listeners unblocks the accept loops; closing quit
// (and, through it, each session's connection) unblocks the reads.
//
// When Serve was never started (a bind failed at boot, before Serve ran), there are
// no goroutines to drain, so Shutdown closes the listeners and returns without
// waiting — which also avoids racing a wg.Wait against Serve's first Add.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.closeListeners()
	e.quitOnce.Do(func() { close(e.quit) })

	e.mu.Lock()
	serving := e.serving
	e.mu.Unlock()
	if !serving {
		return nil
	}

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("smpp engine drain: %w", ctx.Err())
	}
}

func (e *Engine) closeListeners() {
	for _, v := range e.smscs {
		// best-effort: a listener already closed by a prior call returns an error we
		// do not care about during teardown.
		_ = v.listener.Close()
	}
}

// acceptLoop accepts connections for one virtual SMSC until its listener closes.
func (e *Engine) acceptLoop(v *virtualSMSC) {
	for {
		conn, err := v.listener.Accept()
		if err != nil {
			// Accept fails precisely when Shutdown closed the listener; that is the
			// loop's normal exit, not an error to report.
			return
		}
		id := v.bindSeq.Add(1)
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			// Last-resort panic boundary, scoped to this one session: a panic here must
			// not escape to crash the process or the other virtual SMSCs (S6/T1).
			defer e.recoverSession(v, id)
			newSession(id, conn, v, e.quit).run()
		}()
	}
}

// recoverSession is the last-resort panic boundary for a single session goroutine.
// A panic in one session (a codec bug, say) must never take down the process, its
// accept loop, or the sibling virtual SMSCs — so it recovers here, logs loudly with
// the offending virtual SMSC, bind id, panic value and stack, and lets the goroutine
// unwind cleanly (wg.Done, deferred outside, still runs). It is deliberately scoped to
// one session: never a global recover that would mask a determinism bug across
// instances (S6/T1, CLAUDE.md "recover de dernier ressort par SMSC virtuel").
func (e *Engine) recoverSession(v *virtualSMSC, id uint64) {
	if r := recover(); r != nil {
		e.logger.Error("session panic recovered",
			slog.String("virtual_smsc", v.cfg.Name),
			slog.Uint64("bind_id", id),
			slog.Any("panic", r),
			slog.String("stack", string(debug.Stack())))
	}
}

// Addr reports the listen address of the named virtual SMSC, with the real port
// resolved when the config asked for port 0. Tests use it to dial an ephemeral port.
func (e *Engine) Addr(name string) (net.Addr, bool) {
	v, ok := e.byName[name]
	if !ok {
		return nil, false
	}
	return v.listener.Addr(), true
}

// --- observability.Inspector ---

// VirtualSMSCs lists every hosted virtual SMSC.
func (e *Engine) VirtualSMSCs() []observability.VirtualSMSCView {
	out := make([]observability.VirtualSMSCView, 0, len(e.smscs))
	for _, v := range e.smscs {
		out = append(out, v.view())
	}
	return out
}

// VirtualSMSC returns one virtual SMSC's summary, or false if id is unknown.
func (e *Engine) VirtualSMSC(id string) (observability.VirtualSMSCView, bool) {
	v, ok := e.byName[id]
	if !ok {
		return observability.VirtualSMSCView{}, false
	}
	return v.view(), true
}

// ReceivedPDUs returns the recorded submit_sm PDUs of one virtual SMSC, filtered.
func (e *Engine) ReceivedPDUs(id string, f observability.PDUFilter) ([]observability.RecordedPDUView, bool) {
	v, ok := e.byName[id]
	if !ok {
		return nil, false
	}
	records := v.recorder.Snapshot(recorder.Filter(f))
	out := make([]observability.RecordedPDUView, 0, len(records))
	for _, r := range records {
		out = append(out, observability.RecordedPDUView(r))
	}
	return out, true
}

// Binds returns the active bind sessions of one virtual SMSC.
func (e *Engine) Binds(id string) ([]observability.BindView, bool) {
	v, ok := e.byName[id]
	if !ok {
		return nil, false
	}
	return v.binds.views(), true
}

// LogicalClock returns the current global submit_sm count of one virtual SMSC.
func (e *Engine) LogicalClock(id string) (uint64, bool) {
	v, ok := e.byName[id]
	if !ok {
		return 0, false
	}
	return v.logicalClock.Load(), true
}

// DLRsDropped returns how many delivery receipts one virtual SMSC could not emit for a
// mapping reason (e.g. a transmitter-only origin bind). It is a read-only counter, not
// wired to an HTTP route at S4 — DLR inspection and Prometheus metrics land at S6.
func (e *Engine) DLRsDropped(id string) (uint64, bool) {
	v, ok := e.byName[id]
	if !ok {
		return 0, false
	}
	return v.dlrDropped.Load(), true
}
