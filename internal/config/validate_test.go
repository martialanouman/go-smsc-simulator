package config_test

import (
	"errors"
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
