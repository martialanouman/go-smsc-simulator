package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr error  // sentinel to match with errors.Is, when there is one
		errHint string // substring the message must carry, so the operator can act on it
		check   func(t *testing.T, cfg *config.Config)
	}{
		{
			name:    "no path given",
			path:    "",
			wantErr: config.ErrNoConfigPath,
		},
		{
			name:    "file does not exist",
			path:    filepath.Join("testdata", "does-not-exist.yml"),
			wantErr: os.ErrNotExist,
			errHint: "does-not-exist.yml",
		},
		{
			name:    "malformed YAML",
			path:    filepath.Join("testdata", "invalid-syntax.yml"),
			errHint: "invalid-syntax.yml",
		},
		{
			name: "unknown key is rejected",
			path: filepath.Join("testdata", "unknown-key.yml"),
			// The whole point of KnownFields: name the typo rather than ignore it.
			errHint: "htp_prot",
		},
		{
			name: "wrong scalar type",
			path: filepath.Join("testdata", "wrong-type.yml"),
			// yaml.v3 reports the line and the offending value rather than the
			// field name; both locate the fault well enough to act on.
			errHint: "not-a-port",
		},
		{
			name: "minimal fixture loads",
			path: filepath.Join("..", "..", "examples", "minimal.yml"),
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Observability == nil {
					t.Fatal("Observability is nil, want the block to be parsed")
				}
				if got, want := cfg.Observability.HTTPPort, 9000; got != want {
					t.Errorf("HTTPPort = %d, want %d", got, want)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(tc.path)

			if tc.wantErr == nil && tc.errHint == "" {
				if err != nil {
					t.Fatalf("Load(%q) = %v, want no error", tc.path, err)
				}
				tc.check(t, cfg)
				return
			}

			if err == nil {
				t.Fatalf("Load(%q) = nil error, want a failure", tc.path)
			}
			if cfg != nil {
				t.Errorf("Load(%q) returned a config alongside an error, want nil", tc.path)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("Load(%q) error = %v, want it to wrap %v", tc.path, err, tc.wantErr)
			}
			if tc.errHint != "" && !strings.Contains(err.Error(), tc.errHint) {
				t.Errorf("Load(%q) error = %q, want it to name %q", tc.path, err, tc.errHint)
			}
		})
	}
}

// TestLoad_ObservabilityOmittedIsBlackBox pins the absent-vs-zero distinction:
// no observability block must stay distinguishable from http_port: 0, because
// the two mean opposite things (no server at all vs. an ephemeral port).
func TestLoad_ObservabilityOmittedIsBlackBox(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "observability-omitted.yml", "{}\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}
	if cfg.Observability != nil {
		t.Errorf("Observability = %+v, want nil when the block is omitted", cfg.Observability)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "empty.yml", "")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want an empty file to load as an empty config", err)
	}
	if cfg.Observability != nil {
		t.Errorf("Observability = %+v, want nil", cfg.Observability)
	}
}

// TestLoad_Examples keeps every shipped fixture honest: examples/ holds only
// valid, runnable configurations (plan §5). Invalid corpora live in testdata/.
func TestLoad_Examples(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yml"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixture found under examples/, want at least minimal.yml")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			if _, err := config.Load(path); err != nil {
				t.Errorf("Load(%q) = %v, want every shipped example to load", path, err)
			}
		})
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
