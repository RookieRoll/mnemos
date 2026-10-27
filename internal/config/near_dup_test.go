package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

func TestNearDuplicateWindowConfiguration(t *testing.T) {
	for _, tc := range []struct {
		body, want string
		invalid    bool
	}{
		{"", "24h", false},
		{"[search]\nnear_dup_window = \"0s\"\n", "0s", false},
		{"[search]\nnear_dup_window = \"2h\"\n", "2h", false},
		{"[search]\nnear_dup_window = \"-1h\"\n", "", true},
		{"[search]\nnear_dup_window = \"later\"\n", "", true},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadReadOnly(path)
			if (err != nil) != tc.invalid {
				t.Fatalf("load: %v", err)
			}
			if err == nil && cfg.Search.NearDupWindow != tc.want {
				t.Fatalf("window=%q want=%q", cfg.Search.NearDupWindow, tc.want)
			}
		})
	}
}
