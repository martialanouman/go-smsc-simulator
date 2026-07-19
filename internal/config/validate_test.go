package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

// TestValidate_Rules is the S1 acceptance vehicle: each invalid fixture fails at
// load with its specific sentinel, and the message names the offending .yml field.
func TestValidate_Rules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    string
		wantErr error
		errHint string // substring naming the offending field, so an operator can act
	}{
		{"unknown profile", "unknown-profile.yml", config.ErrUnknownProfile, "profile"},
		{"seeded wallclock", "seeded-wallclock.yml", config.ErrSeededWallclock, "clock"},
		{"duplicate port", "duplicate-port.yml", config.ErrDuplicatePort, "port"},
		{"param out of bounds", "param-out-of-bounds.yml", config.ErrParamOutOfBounds, "max_ms"},
		{"unknown to_profile", "unknown-to-profile.yml", config.ErrUnknownToProfile, "to_profile"},
		{"param not exposed", "param-not-exposed.yml", config.ErrParamNotExposed, "success_rate"},
		{"invalid address_range", "invalid-address-range.yml", config.ErrInvalidAddressRange, "address_range"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("testdata", tc.file)
			cfg, err := config.Load(path)

			if err == nil {
				t.Fatalf("Load(%q) = nil error, want a validation failure", path)
			}
			if cfg != nil {
				t.Errorf("Load(%q) returned a config alongside an error, want nil", path)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Load(%q) error = %v, want it to wrap %v", path, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.errHint) {
				t.Errorf("Load(%q) error = %q, want it to name %q", path, err, tc.errHint)
			}
		})
	}
}

// baseSMSC is a minimal, valid single-SMSC config prefix. Coherence cases append a
// tail (scenario + optional scheduled blocks) to exercise one validation branch.
const baseSMSC = `virtual_smscs:
  - name: carrier-a
    port: 2775
    bind_credentials: { system_id: c1, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 42
    pdu_buffer_size: 10000
`

// TestValidate_SchemaCoherence covers the schema-coherence branches beyond the five
// headline rules: DLR, MO injection, latency params, scheduled-disconnect enums and
// the observability/SMPP port collision. Each case trips exactly one branch and
// asserts the sentinel plus the field the message must name.
func TestValidate_SchemaCoherence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string // full config when set, else baseSMSC + tail
		tail    string
		wantErr error
		errHint string
	}{
		{name: "dlr non-fixed distribution", wantErr: config.ErrInvalidEnum, errHint: "distribution", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
      dlr:
        delay: { distribution: normal, ticks: 5 }
        outcome_weights: { delivered: 1 }`},
		{name: "dlr fixed missing ticks", wantErr: config.ErrMissingParam, errHint: "ticks", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
      dlr:
        delay: { distribution: fixed }
        outcome_weights: { delivered: 1 }`},
		{name: "dlr stray min_ticks under fixed", wantErr: config.ErrParamNotExposed, errHint: "min_ticks", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
      dlr:
        delay: { distribution: fixed, ticks: 5, min_ticks: 2 }
        outcome_weights: { delivered: 1 }`},
		{name: "dlr outcome weights sum zero", wantErr: config.ErrParamOutOfBounds, errHint: "outcome_weights", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
      dlr:
        delay: { distribution: fixed, ticks: 5 }
        outcome_weights: { delivered: 0, failed: 0, expired: 0 }`},
		{name: "dlr fixed ticks zero", wantErr: config.ErrParamOutOfBounds, errHint: "ticks", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
      dlr:
        delay: { distribution: fixed, ticks: 0 }
        outcome_weights: { delivered: 1 }`},
		{name: "latency missing param", wantErr: config.ErrMissingParam, errHint: "max_ms", tail: `    scenario:
      profile: healthy
      latency: { distribution: uniform, params: { min_ms: 10 } }`},
		{name: "latency extra param", wantErr: config.ErrParamNotExposed, errHint: "min_ms", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20, min_ms: 10 } }`},
		{name: "latency invalid distribution", wantErr: config.ErrInvalidEnum, errHint: "distribution", tail: `    scenario:
      profile: healthy
      latency: { distribution: bogus, params: { ms: 20 } }`},
		{name: "latency min above max", wantErr: config.ErrParamOutOfBounds, errHint: "min_ms", tail: `    scenario:
      profile: healthy
      latency: { distribution: uniform, params: { min_ms: 30, max_ms: 10 } }`},
		{name: "mo auto missing rate", wantErr: config.ErrMissingParam, errHint: "rate_per_sec", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: auto, content_template: "x" }`},
		{name: "mo auto missing template", wantErr: config.ErrMissingParam, errHint: "content_template", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: auto, rate_per_sec: 5 }`},
		{name: "mo scheduled missing events", wantErr: config.ErrMissingParam, errHint: "events", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: scheduled }`},
		{name: "mo scheduled stray rate", wantErr: config.ErrParamNotExposed, errHint: "rate_per_sec", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: scheduled, rate_per_sec: 5, events: [ { at_tick: 1, source_addr: "1", dest_addr: "2", content: "c" } ] }`},
		{name: "mo disabled stray events", wantErr: config.ErrParamNotExposed, errHint: "events", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: disabled, events: [ { at_tick: 1, source_addr: "1", dest_addr: "2", content: "c" } ] }`},
		{name: "mo invalid mode", wantErr: config.ErrInvalidEnum, errHint: "mode", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: bogus }`},
		{name: "mo seeded wallclock", wantErr: config.ErrSeededWallclock, errHint: "clock", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    mo_injection: { mode: auto, clock: wallclock, rate_per_sec: 5, content_template: "x" }`},
		{name: "disconnect invalid scope", wantErr: config.ErrInvalidEnum, errHint: "scope", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    scheduled_disconnects: [ { at_tick: 1, scope: bogus, when: before_response } ]`},
		{name: "disconnect invalid when", wantErr: config.ErrInvalidEnum, errHint: "when", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    scheduled_disconnects: [ { at_tick: 1, scope: all, when: bogus } ]`},
		{name: "error_mix sum zero", wantErr: config.ErrParamOutOfBounds, errHint: "error_mix", tail: `    scenario:
      profile: flaky-carrier
      params: { error_mix: {} }
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "error_mix invalid key", wantErr: config.ErrInvalidEnum, errHint: "error_mix", tail: `    scenario:
      profile: flaky-carrier
      params: { error_mix: { NOT_A_CODE: 1 } }
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "seeded throughput_limit on deterministic profile", wantErr: config.ErrSeededThroughputLimit, errHint: "throughput_limit_per_sec", tail: `    throughput_limit_per_sec: 100
    scenario:
      profile: flaky-carrier
      params: { success_rate: 0.9, error_mix: { ESME_RSYSERR: 1 } }
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "seeded transition into throughput profile", wantErr: config.ErrSeededThroughputLimit, errHint: "scheduled_transitions[0].to_profile", tail: `    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }
    scheduled_transitions: [ { at_tick: 100, to_profile: throttling-carrier } ]`},
		{name: "throughput-capped without cap", wantErr: config.ErrMissingParam, errHint: "throughput_cap_per_sec", tail: `    scenario:
      profile: throughput-capped
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "quiescence_flush_ms zero", wantErr: config.ErrParamOutOfBounds, errHint: "quiescence_flush_ms", tail: `    quiescence_flush_ms: 0
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "tls cert_file without key_file", wantErr: config.ErrTLSCertKeyMismatch, errHint: "cert_file", tail: `    tls: { enabled: true, cert_file: /tmp/nope.pem }
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "tls cert files missing", wantErr: config.ErrTLSCertNotReadable, errHint: "cert_file", tail: `    tls: { enabled: true, cert_file: /does/not/exist.pem, key_file: /does/not/exist.key }
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "tls cert set but disabled", wantErr: config.ErrTLSCertWithoutEnabled, errHint: "tls", tail: `    tls: { enabled: false, cert_file: /does/not/exist.pem, key_file: /does/not/exist.key }
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`},
		{name: "observability port collides with smsc", wantErr: config.ErrDuplicatePort, errHint: "observability.http_port", yaml: `observability:
  http_port: 2775
virtual_smscs:
  - name: carrier-a
    port: 2775
    bind_credentials: { system_id: c1, password: secret }
    addr_ton: 1
    addr_npi: 1
    seed: 42
    pdu_buffer_size: 10000
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := tc.yaml
			if content == "" {
				content = baseSMSC + tc.tail
			}
			path := writeTemp(t, "coherence.yml", content)

			cfg, err := config.Load(path)
			if err == nil {
				t.Fatalf("Load(%s) = nil error, want a validation failure", tc.name)
			}
			if cfg != nil {
				t.Errorf("Load(%s) returned a config alongside an error, want nil", tc.name)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Load(%s) error = %v, want it to wrap %v", tc.name, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.errHint) {
				t.Errorf("Load(%s) error = %q, want it to name %q", tc.name, err, tc.errHint)
			}
		})
	}
}

// TestValidate_AggregatesAllErrors pins the aggregation decision: a config broken on
// two counts reports both sentinels at once, not just the first.
func TestValidate_AggregatesAllErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join("testdata", "multiple-faults.yml")
	cfg, err := config.Load(path)

	if err == nil {
		t.Fatalf("Load(%q) = nil error, want a validation failure", path)
	}
	if cfg != nil {
		t.Errorf("Load(%q) returned a config alongside an error, want nil", path)
	}
	if !errors.Is(err, config.ErrDuplicatePort) {
		t.Errorf("Load(%q) error = %v, want it to wrap ErrDuplicatePort", path, err)
	}
	if !errors.Is(err, config.ErrUnknownProfile) {
		t.Errorf("Load(%q) error = %v, want it to wrap ErrUnknownProfile", path, err)
	}
	if got := err.Error(); !strings.Contains(got, "port") || !strings.Contains(got, "not-a-carrier") {
		t.Errorf("Load(%q) error = %q, want it to name both faults", path, got)
	}
}

// TestLoad_ParsesFullSchema loads a config exercising every §3.1 block and asserts
// the pointer/absent-vs-zero semantics survive decoding.
func TestLoad_ParsesFullSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join("testdata", "full-schema.yml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) = %v, want the full-schema fixture to load", path, err)
	}
	if len(cfg.VirtualSMSCs) != 1 {
		t.Fatalf("len(VirtualSMSCs) = %d, want 1", len(cfg.VirtualSMSCs))
	}
	vs := cfg.VirtualSMSCs[0]

	if vs.Seed == nil || *vs.Seed != 42 {
		t.Errorf("Seed = %v, want non-nil 42", vs.Seed)
	}
	if vs.ThroughputLimitPerSec == nil || *vs.ThroughputLimitPerSec != 5000 {
		t.Errorf("ThroughputLimitPerSec = %v, want non-nil 5000", vs.ThroughputLimitPerSec)
	}
	if vs.QuiescenceFlushMs == nil || *vs.QuiescenceFlushMs != 100 {
		t.Errorf("QuiescenceFlushMs = %v, want non-nil 100", vs.QuiescenceFlushMs)
	}
	if got := vs.EffectiveQuiescenceFlushMs(); got != 100 {
		t.Errorf("EffectiveQuiescenceFlushMs = %d, want 100", got)
	}
	if !vs.TLS.Enabled {
		t.Error("TLS.Enabled = false, want true")
	}
	if vs.Scenario.DLR == nil {
		t.Error("Scenario.DLR = nil, want the dlr block parsed")
	}
	if vs.MOInjection == nil {
		t.Fatal("MOInjection = nil, want the mo_injection block parsed")
	}
	if len(vs.MOInjection.Events) != 1 || vs.MOInjection.Events[0].AtTick != 100 {
		t.Errorf("MOInjection.Events = %+v, want one event at tick 100", vs.MOInjection.Events)
	}
	if len(vs.ScheduledTransitions) != 2 {
		t.Fatalf("len(ScheduledTransitions) = %d, want 2", len(vs.ScheduledTransitions))
	}
	if got := vs.ScheduledTransitions[0].ToProfile; got != config.ProfileDeadCarrier {
		t.Errorf("ScheduledTransitions[0].ToProfile = %q, want %q", got, config.ProfileDeadCarrier)
	}
	if got := vs.ScheduledTransitions[1].ToProfile; got != config.ProfileHealthy {
		t.Errorf("ScheduledTransitions[1].ToProfile = %q, want %q", got, config.ProfileHealthy)
	}
}

// TestLoad_TLSSuppliedCertFiles pins the supplied-cert config path: cert_file/key_file
// decode and validate when both files exist. Validation checks existence only — the PEM
// parse is deliberately deferred to engine boot — so dummy files suffice here.
func TestLoad_TLSSuppliedCertFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}

	content := baseSMSC + `    tls:
      enabled: true
      cert_file: ` + certPath + `
      key_file: ` + keyPath + `
    scenario:
      profile: healthy
      latency: { distribution: fixed, params: { ms: 20 } }`
	path := writeTemp(t, "tls-supplied.yml", content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load with supplied cert files = %v, want success", err)
	}
	vs := cfg.VirtualSMSCs[0]
	if !vs.TLS.Enabled || vs.TLS.CertFile != certPath || vs.TLS.KeyFile != keyPath {
		t.Errorf("TLS = %+v, want enabled with cert_file=%q key_file=%q", vs.TLS, certPath, keyPath)
	}
}

// TestValidate_EmptyAndMinimalStayValid is a regression guard: a config with no
// virtual_smscs (empty file, or observability-only) still validates to nil — the
// black-box mode must survive the new validation phase.
func TestValidate_EmptyAndMinimalStayValid(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty file":          "",
		"observability only":  "observability:\n  http_port: 9000\n",
		"explicit empty maps": "{}\n",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeTemp(t, "regression.yml", content)
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("Load() = %v, want a config with no virtual_smscs to validate", err)
			}
			if len(cfg.VirtualSMSCs) != 0 {
				t.Errorf("len(VirtualSMSCs) = %d, want 0", len(cfg.VirtualSMSCs))
			}
		})
	}
}
