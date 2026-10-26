package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/vault"
)

// homeWithConfig prepares an isolated home directory and writes a config
// file that points at the given embedding provider + base_url. Returns
// the home dir. Sets both HOME (unix) and USERPROFILE (windows) so tests
// are portable — os.UserHomeDir consults each depending on GOOS.
func homeWithConfig(t *testing.T, embedConfig string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// A developer's own MNEMOS_CONFIG must not steer tests at their real store.
	t.Setenv("MNEMOS_CONFIG", "")
	cfgDir := filepath.Join(home, ".mnemos")
	_ = os.MkdirAll(cfgDir, 0o755)
	if embedConfig != "" {
		_ = os.WriteFile(
			filepath.Join(cfgDir, "config.toml"),
			[]byte(embedConfig),
			0o644,
		)
	}
	return home
}

func TestRunDreamOneShot(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runDream(context.Background(), nil); err != nil {
			t.Fatalf("dream: %v", err)
		}
	})
	if !strings.Contains(out, "dream pass") {
		t.Errorf("dream summary missing: %s", out)
	}
}

func TestRunVaultExport(t *testing.T) {
	home := homeWithConfig(t, "[vault]\nenabled = true\n")
	vaultDir := filepath.Join(home, "myvault")
	out := captureStdout(t, func() {
		if err := runVaultExport(context.Background(), []string{"--out", vaultDir}); err != nil {
			t.Fatalf("vault export: %v", err)
		}
	})
	if !strings.Contains(out, "exported") {
		t.Errorf("vault export output: %s", out)
	}
	// The vault dir should now exist even if empty.
	if _, err := os.Stat(vaultDir); err != nil {
		t.Errorf("vault dir not created: %v", err)
	}
}

func TestRunEmbedStatusReturnsDisabled(t *testing.T) {
	homeWithConfig(t, `
[embedding]
provider = "none"
`)
	out := captureStdout(t, func() {
		_ = runEmbedStatus(context.Background())
	})
	if !strings.Contains(out, "enabled:  false") {
		t.Errorf("expected enabled: false, got: %s", out)
	}
}

func TestRunEmbedBackfillWithFakeOllama(t *testing.T) {
	// Fake Ollama endpoint that always returns a 4-dim vector.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { // probe
			w.WriteHeader(200)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3, 0.4}},
		})
	}))
	defer srv.Close()

	homeWithConfig(t, `
[embedding]
provider = "ollama"
model = "test-model"
dimension = 4
base_url = "`+srv.URL+`"
`)

	out := captureStdout(t, func() {
		_ = runEmbedBackfill(context.Background())
	})
	if !strings.Contains(out, "backfilled") {
		t.Errorf("backfill output: %s", out)
	}
}

func TestSelectEmbedderVariants(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.EmbeddingConfig
		wantDim   int
		wantModel string
	}{
		{
			name:      "none",
			cfg:       config.EmbeddingConfig{Provider: "none"},
			wantDim:   0,
			wantModel: "none",
		},
		{
			name:      "ollama explicit",
			cfg:       config.EmbeddingConfig{Provider: "ollama", Model: "m", Dimension: 4},
			wantDim:   4,
			wantModel: "ollama/m",
		},
		{
			name:      "openai explicit",
			cfg:       config.EmbeddingConfig{Provider: "openai", Model: "x", Dimension: 8},
			wantDim:   8,
			wantModel: "openai/x",
		},
		{
			name:      "auto with no ollama",
			cfg:       config.EmbeddingConfig{Provider: "auto", BaseURL: "http://127.0.0.1:1"},
			wantDim:   0,
			wantModel: "none",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := selectEmbedder(context.Background(), tc.cfg)
			if e.Dimension() != tc.wantDim {
				t.Errorf("dim: want %d, got %d", tc.wantDim, e.Dimension())
			}
			if e.Model() != tc.wantModel {
				t.Errorf("model: want %q, got %q", tc.wantModel, e.Model())
			}
		})
	}
}

func TestRunSessionsEmpty(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		_ = runSessions(context.Background(), nil)
	})
	if !strings.Contains(out, "no sessions yet") {
		t.Errorf("empty sessions output: %s", out)
	}
}

func TestRunVaultUnknownSubcommand(t *testing.T) {
	if err := runVault(context.Background(), []string{"badcmd"}); err == nil {
		t.Error("unknown vault subcommand must error")
	}
}

func TestRunEmbedUnknownSubcommand(t *testing.T) {
	if err := runEmbed(context.Background(), []string{"badcmd"}); err == nil {
		t.Error("unknown embed subcommand must error")
	}
	if err := runEmbed(context.Background(), nil); err == nil {
		t.Error("empty embed args must error")
	}
}

func TestRunVaultStatusAfterExport(t *testing.T) {
	homeWithConfig(t, "[vault]\nenabled = true\n")
	captureStdout(t, func() {
		if err := runVaultExport(context.Background(), nil); err != nil {
			t.Fatalf("vault export: %v", err)
		}
	})
	out := captureStdout(t, func() {
		if err := runVaultStatus(context.Background(), nil); err != nil {
			t.Fatalf("vault status: %v", err)
		}
	})
	if strings.Contains(out, "last_exported_at: never") {
		t.Errorf("status ignored the export manifest:\n%s", out)
	}
	if !strings.Contains(out, "pending 0") {
		t.Errorf("status should report nothing pending after export:\n%s", out)
	}
	if !strings.Contains(out, "watcher: stopped") {
		t.Errorf("status should report the watcher stopped:\n%s", out)
	}
}

func TestRunVaultStatusCreatesNothing(t *testing.T) {
	home := homeWithConfig(t, "[vault]\nenabled = true\n")
	vaultDir := filepath.Join(home, ".mnemos", "vault")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := runVaultStatus(context.Background(), nil); err != nil {
			t.Fatalf("vault status: %v", err)
		}
	})
	after, err := os.ReadDir(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("status changed the vault directory: before=%v after=%v", before, after)
	}
}

func TestRunVaultStatusNamesStaleHeartbeat(t *testing.T) {
	home := homeWithConfig(t, "[vault]\nenabled = true\n")
	vaultDir := filepath.Join(home, ".mnemos", "vault")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-time.Hour)
	if err := vault.WriteWatchHeartbeat(vaultDir, vault.WatchHeartbeat{
		PID: 99, StartedAt: stale, HeartbeatAt: stale,
	}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runVaultStatus(context.Background(), nil); err != nil {
			t.Fatalf("vault status: %v", err)
		}
	})
	if !strings.Contains(out, "watcher: stopped (last heartbeat "+stale.Format(time.RFC3339)+")") {
		t.Fatalf("status did not name the stale heartbeat time:\n%s", out)
	}
}

func TestRunVaultStatusBeforeExport(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		_ = runVaultStatus(context.Background(), nil)
	})
	// A vault directory mnemos never wrote to must say so instead of looking
	// healthy: no manifest is "never", the store is absent so pending is
	// unknown, and no heartbeat is "stopped".
	for _, want := range []string{
		"vault path",
		"directory:  missing",
		"last_exported_at: never",
		"pending: unknown",
		"watcher: stopped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}
