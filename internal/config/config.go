// Package config loads the simulator's single source of configuration: one YAML
// file, read once at startup.
//
// The package exposes no mutation API on purpose. Reconfiguring the simulator
// means editing the file and restarting it; there is no runtime reconfiguration
// path anywhere in the process (invariant b, plan §0.5).
package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the whole simulator configuration, materialised from the YAML file.
//
// It is immutable once Load returns: callers read it, never write it. Every
// value here is decided, and fully validated, before a single port is opened
// (invariant b). The struct tree for virtual_smscs and its scenario/schedule
// blocks lives in schema.go; the enums in enums.go; the validation in validate.go.
type Config struct {
	// Observability is nil when the block is absent from the YAML, which
	// disables the HTTP surface entirely — the "black box" mode of spec §5.2.
	// Absent and zero are different states, hence the pointer.
	Observability *ObservabilityConfig `yaml:"observability"`

	// VirtualSMSCs is the topology: zero or more virtual SMSCs, each on its own
	// port. Absent (nil) is a valid, black-box configuration serving no SMPP.
	VirtualSMSCs []VirtualSMSCConfig `yaml:"virtual_smscs"`
}

// ObservabilityConfig describes the process-wide read-only HTTP surface.
// One port serves the whole process, not one per virtual SMSC (plan §1.4).
type ObservabilityConfig struct {
	// HTTPPort is the port serving /health, the read-only inspection endpoints
	// (S2) and /metrics (S6). Port 0 asks the OS for an ephemeral port.
	HTTPPort int `yaml:"http_port"`
}

// ErrNoConfigPath is returned when no configuration file was designated.
// The simulator has no default configuration: a missing --config is fatal.
var ErrNoConfigPath = errors.New("no config path given")

// Load reads, parses and fully validates the YAML configuration at path.
//
// fail-fast: Load is the boot gate. Every error it can report — syntax, unknown
// key, or a broken §3.1 rule (unknown profile, duplicate port, seed/clock
// coherence, out-of-bounds param) — happens before the process binds anything, so
// an invalid file can never leave a half-open listener behind (invariant b).
// Unknown YAML keys are rejected: a key the schema does not know is a typo, and a
// typo silently ignored is a test lying about what it exercised.
//
// On any failure Load returns (nil, err): no half-validated config leaks out.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, ErrNoConfigPath
	}

	f, err := os.Open(path) //nolint:gosec // the config path is operator-supplied by design
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer func() {
		_ = f.Close() // best-effort on teardown: the bytes are already decoded
	}()

	cfg, err := decode(f, path)
	if err != nil {
		return nil, err
	}

	// fail-fast: rejected at config load. Business validation runs before Load
	// returns, so run() can never reach its boot gate with an invalid topology.
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}

	return cfg, nil
}

// decode parses YAML from r. It is split out of Load so tests can exercise the
// parser without touching the filesystem; name is only used to locate errors.
func decode(r io.Reader, name string) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file parses to nothing. Treat it as an explicit choice:
			// no observability block, no HTTP server, no virtual SMSCs.
			return &Config{}, nil
		}
		return nil, fmt.Errorf("parse config %s: %w", name, err)
	}

	return &cfg, nil
}
