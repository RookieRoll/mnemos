package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/config"
)

func TestMaintenanceDefaultsAndGeneratedTOML(t *testing.T) {
	defaults := config.Default()
	if defaults.MCP.Profile != "core" {
		t.Fatalf("MCP profile default = %q, want core", defaults.MCP.Profile)
	}
	got := defaults.Maintenance
	if !got.Enabled || got.Interval != 24*time.Hour || got.MaxPassDuration != 5*time.Second || got.LeaseDuration != 15*time.Second || got.RenewInterval != 2*time.Second {
		t.Fatalf("maintenance defaults = %+v", got)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[mcp]", `profile = "core"`, "[maintenance]", "enabled = true",
		`interval = "24h0m0s"`, `max_pass_duration = "5s"`,
		`lease_duration = "15s"`, `renew_interval = "2s"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("generated config missing %q", want)
		}
	}
}

func TestLoadAcceptsCustomMCPProfileAndMaintenanceDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `[mcp]
profile = "maintenance"

[maintenance]
enabled = false
interval = "30m"
max_pass_duration = "3s"
lease_duration = "20s"
renew_interval = "4s"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.MCP.Profile != "maintenance" {
		t.Errorf("profile = %q, want maintenance", got.MCP.Profile)
	}
	want := config.MaintenanceConfig{
		Enabled: false, Interval: 30 * time.Minute, MaxPassDuration: 3 * time.Second,
		LeaseDuration: 20 * time.Second, RenewInterval: 4 * time.Second,
	}
	if got.Maintenance != want {
		t.Errorf("maintenance = %+v, want %+v", got.Maintenance, want)
	}
}

func TestLoadRejectsInvalidMCPProfileAndMaintenanceBudgets(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown profile", body: "[mcp]\nprofile = \"dynamic\"\n"},
		{name: "empty profile", body: "[mcp]\nprofile = \"\"\n"},
		{name: "zero interval", body: "[maintenance]\ninterval = \"0s\"\n"},
		{name: "negative maximum", body: "[maintenance]\nmax_pass_duration = \"-1s\"\n"},
		{name: "zero lease", body: "[maintenance]\nlease_duration = \"0s\"\n"},
		{name: "negative renewal", body: "[maintenance]\nrenew_interval = \"-1s\"\n"},
		{name: "pass reaches lease", body: "[maintenance]\nmax_pass_duration = \"15s\"\n"},
		{name: "renewal reaches lease", body: "[maintenance]\nrenew_interval = \"15s\"\n"},
		{name: "pass exceeds cap", body: "[maintenance]\nmax_pass_duration = \"5m1s\"\n"},
		{name: "lease exceeds cap", body: "[maintenance]\nlease_duration = \"10m1s\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.LoadReadOnly(path); err == nil {
				t.Fatal("expected config validation error")
			}
		})
	}
}
