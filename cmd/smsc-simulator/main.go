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
	_ = observability.NewRegistry() // STUB S6: no collector registered, no /metrics served yet. See plan §10.

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- boot gate: the config is valid; only now may the process bind ports ---

	srv, err := startObservability(cfg, logger)
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	if srv != nil {
		go func() { serveErr <- srv.Serve() }()
	}

	logger.Info("smsc-simulator started", slog.String("config", configPath))

	select {
	case err := <-serveErr:
		// The surface died on its own: report it rather than hang waiting for a
		// signal that would never come.
		if err != nil {
			return err
		}
		return errors.New("observability surface stopped unexpectedly")
	case <-ctx.Done():
	}

	logger.Info("shutting down")

	// WithoutCancel keeps ctx's values but drops its cancellation: ctx has just
	// fired, and a drain deadline derived from it would expire before the drain
	// began, turning the graceful stop into an abrupt one.
	return shutdown(context.WithoutCancel(ctx), srv, serveErr)
}

// startObservability brings up the read-only surface, or returns (nil, nil) when
// the observability block is absent — the "black box" mode of spec §5.2, where
// the simulator exposes no HTTP at all.
func startObservability(cfg *config.Config, logger *slog.Logger) (*observability.Server, error) {
	if cfg.Observability == nil {
		logger.Info("no observability block: running as a black box, no HTTP surface")
		return nil, nil //nolint:nilnil // absence of a server is a valid, expected outcome here
	}

	srv, err := observability.NewServer(cfg.Observability.HTTPPort, logger)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

// shutdown drains the surface within shutdownTimeout and reports whatever Serve
// returned, so a failure during teardown is not swallowed.
//
// ctx must not already be cancelled; see the call site.
func shutdown(ctx context.Context, srv *observability.Server, serveErr <-chan error) error {
	if srv == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	return <-serveErr
}
