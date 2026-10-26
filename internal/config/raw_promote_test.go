package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

// rawPromoteConfigFile materialises a TOML body so each case of the tables
// below loads a real file instead of a hand-built struct — the switch is a
// loading contract, not a struct-field test.
func rawPromoteConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigRawPromote_DefaultsToAuto(t *testing.T) {
	if got := config.Default().Rumination.RawPromote; got != config.RawPromoteAuto {
		t.Fatalf("Default() raw_promote = %q, want %q", got, config.RawPromoteAuto)
	}

	// The generated config is documentation: an operator who opens it must see
	// the switch and its default without reading the source.
	generated := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Load(generated); err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	body, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "raw_promote = \"auto\"") {
		t.Errorf("generated config does not advertise the raw_promote default:\n%s", body)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "empty file", body: ""},
		{name: "rumination section without key", body: "[rumination]\nenabled = true\n"},
		{name: "other rumination keys only", body: "[rumination]\nstale_raw_days = 30\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.LoadReadOnly(rawPromoteConfigFile(t, tt.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Rumination.RawPromote != config.RawPromoteAuto {
				t.Fatalf("raw_promote = %q, want %q", cfg.Rumination.RawPromote, config.RawPromoteAuto)
			}
		})
	}
}

func TestConfigRawPromote_AcceptsAllowedValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "auto", body: "[rumination]\nraw_promote = \"auto\"\n", want: config.RawPromoteAuto},
		{name: "never", body: "[rumination]\nraw_promote = \"never\"\n", want: config.RawPromoteNever},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.LoadReadOnly(rawPromoteConfigFile(t, tt.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Rumination.RawPromote != tt.want {
				t.Fatalf("raw_promote = %q, want %q", cfg.Rumination.RawPromote, tt.want)
			}
		})
	}
}

func TestConfigRawPromote_RejectsUnknown(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "near miss", value: "sometimes"},
		{name: "wrong case upper", value: "Never"},
		{name: "wrong case lower", value: "AUTO"},
		{name: "trailing space", value: "never "},
		{name: "leading space", value: " auto"},
		{name: "explicit empty", value: ""},
		{name: "boolean-ish false", value: "false"},
		{name: "boolean-ish true", value: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "[rumination]\nraw_promote = \"" + tt.value + "\"\n"
			_, err := config.LoadReadOnly(rawPromoteConfigFile(t, body))
			if err == nil {
				t.Fatalf("raw_promote = %q loaded without error; invalid values must fail rather than fall back", tt.value)
			}
			msg := err.Error()
			if !strings.Contains(msg, "rumination.raw_promote") {
				t.Errorf("error does not name the field: %q", msg)
			}
			for _, allowed := range []string{config.RawPromoteNever, config.RawPromoteAuto} {
				if !strings.Contains(msg, allowed) {
					t.Errorf("error does not list allowed value %q: %q", allowed, msg)
				}
			}
		})
	}
}
