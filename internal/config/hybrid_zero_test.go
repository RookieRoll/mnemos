package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesExplicitHybridAlphaZero(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       float64
	}{
		{"omitted", "[search]\n", 0.5},
		{"zero", "[search]\nhybrid_alpha=0\n", 0},
		{"fraction", "[search]\nhybrid_alpha=0.2\n", 0.2},
		{"one", "[search]\nhybrid_alpha=1\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Search.HybridAlpha != tc.want {
				t.Fatalf("alpha=%v, want %v", got.Search.HybridAlpha, tc.want)
			}
		})
	}
}
