// Command smsc-simulator runs virtual SMSCs described by a YAML file, to test an
// SMS gateway against a controllable, reproducible carrier peer.
//
// It is a test/CI tool. It is never a production component, and it has no
// configuration API: the YAML file passed to --config is the only input, read
// once at startup. Reconfiguring means editing the file and restarting.
//
// STUB S1/S2: at S0 the process loads its config, serves the read-only
// observability surface, and waits for SIGTERM. It hosts no virtual SMSC and
// speaks no SMPP yet; those land at S1 and S2. See plan §5 and §6.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

// shutdownTimeout bounds the graceful stop. A test peer that refuses to die
// wedges the CI job it was meant to serve, so the drain is deliberately short.
const shutdownTimeout = 5 * time.Second

func main() {
	configPath := flag.String("config", "", "path to the YAML configuration file (required)")
	flag.Parse()

	// main stays trivial on purpose: all the logic lives in run, which returns
	// an error instead of exiting. That is what makes startup ordering and
	// graceful shutdown testable in-process, without spawning the binary.
	if err := run(context.Background(), *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "smsc-simulator: %v\n", err)
		if errors.Is(err, config.ErrNoConfigPath) {
			// The simulator has no default configuration, so this is the one
			// error where the fix is a flag the operator simply did not know.
			flag.Usage()
		}
		os.Exit(1)
	}
}

// run boots the simulator and blocks until ctx is cancelled or SIGTERM arrives.
//
// The statement order is the contract, not a style choice: the configuration is
// loaded and validated *before* anything binds a port, so an invalid file can
// never leave a listener open behind a failed boot (invariant b, plan §0.5).
// Nothing above the "boot gate" comment may open a socket.
func run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger := observability.NewLogger(os.Stdout, slog.LevelInfo)

	// STUB S6: the Prometheus registry is created and threaded through once a
	// collector and /metrics endpoint exist to consume it. Constructing one here
	// now would only be a discarded allocation. See plan §10.

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- boot gate: the config is valid; only now may the process bind ports ---

	// The engine binds the SMPP listeners first (fail-fast on a port clash), then the
	// observability surface is built against it as its read-only Inspector.
	engine, err := smsc.New(cfg.VirtualSMSCs, logger)
	if err != nil {
		return err
	}

	srv, err := startObservability(cfg, logger, engine)
	if err != nil {
		// The engine's listeners are already open; close them so a failed boot leaves
		// no half-open SMPP port behind. Serve never ran, so this returns at once.
		stopEngine(context.WithoutCancel(ctx), logger, engine)
		return err
	}

	obsErr := make(chan error, 1)
	if srv != nil {
		go func() { obsErr <- srv.Serve() }()
	}
	engineErr := make(chan error, 1)
	go func() { engineErr <- engine.Serve() }()

	logger.Info("smsc-simulator started", slog.String("config", configPath))

	select {
	case err := <-obsErr:
		// The surface died on its own: report it rather than hang waiting for a
		// signal that would never come.
		if err != nil {
			err = fmt.Errorf("observability surface: %w", err)
		} else {
			err = errors.New("observability surface stopped unexpectedly")
		}
		stopEngine(context.WithoutCancel(ctx), logger, engine)
		return err
	case err := <-engineErr:
		// Serve only returns after Shutdown, so an early return here is a failure.
		_ = shutdown(context.WithoutCancel(ctx), logger, srv, obsErr)
		if err != nil {
			return fmt.Errorf("smpp engine: %w", err)
		}
		return errors.New("smpp engine stopped unexpectedly")
	case <-ctx.Done():
	}

	logger.Info("shutting down")

	// WithoutCancel keeps ctx's values but drops its cancellation: ctx has just
	// fired, and a drain deadline derived from it would expire before the drain
	// began, turning the graceful stop into an abrupt one.
	//
	// Only the observability drain's result is returned: an engine drain overrun is a
	// degraded-but-requested stop, logged inside stopEngine rather than surfaced as a
	// non-zero exit (same contract as shutdown, so SIGTERM never fails CI teardown).
	drainCtx := context.WithoutCancel(ctx)
	stopEngine(drainCtx, logger, engine)
	return shutdown(drainCtx, logger, srv, obsErr)
}

// stopEngine drains the SMPP engine within shutdownTimeout. A drain that overruns
// the deadline is logged, not returned: like the observability drain, a slow but
// operator-requested stop is a degraded success, not a process failure — returning
// it non-zero would fail the CI teardown that sent the signal.
func stopEngine(ctx context.Context, logger *slog.Logger, engine *smsc.Engine) {
	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	if err := engine.Shutdown(ctx); err != nil {
		logger.Warn("smpp engine did not drain within the deadline", slog.Any("error", err))
	}
}

// startObservability brings up the read-only surface bound to insp, or returns
// (nil, nil) when the observability block is absent — the "black box" mode of spec
// §5.2, where the simulator exposes no HTTP at all.
func startObservability(cfg *config.Config, logger *slog.Logger, insp observability.Inspector) (*observability.Server, error) {
	if cfg.Observability == nil {
		logger.Info("no observability block: running as a black box, no HTTP surface")
		return nil, nil //nolint:nilnil // absence of a server is a valid, expected outcome here
	}

	srv, err := observability.NewServer(cfg.Observability.HTTPPort, logger, insp)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

// shutdown drains the surface within shutdownTimeout.
//
// ctx must not already be cancelled; see the call site. A drain that overruns
// its deadline is logged but not returned as an error: shutdown runs only on an
// operator-requested stop (SIGTERM), and a slow-but-requested stop is a degraded
// success, not a process failure — exiting non-zero here would fail the CI
// teardown that sent the signal.
func shutdown(ctx context.Context, logger *slog.Logger, srv *observability.Server, serveErr <-chan error) error {
	if srv == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Warn("observability surface did not drain within the deadline", slog.Any("error", err))
		return nil
	}
	return <-serveErr
}
