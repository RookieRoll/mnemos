package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/memory"
)

func selectedConfig(t *testing.T, body string) (string, context.Context) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selected config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, config.ContextWithPath(context.Background(), path)
}

func TestPiProjectionOmitsSecretsAndDoesNotProbeOrCreateStore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate config.toml")
	body := "[storage]\npath = \"candidate.db\"\n[embedding]\nprovider = \"openai\"\nbase_url = \"http://127.0.0.1:1\"\napi_key = \"secret-provider\"\n[server]\napi_key = \"secret-server\"\n[integration]\ndisabled = true\n[continuity]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := config.ContextWithPath(context.Background(), path)
	out := captureStdout(t, func() {
		if err := runConfig(ctx, []string{"--client", "pi"}); err != nil {
			t.Fatal(err)
		}
	})
	var projection map[string]any
	if err := json.Unmarshal([]byte(out), &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection) != 3 || projection["protocol_version"] != float64(1) || projection["disabled"] != true || projection["continuity"].(map[string]any)["enabled"] != true {
		t.Fatalf("unexpected projection: %s", out)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "127.0.0.1") {
		t.Fatalf("projection disclosed unrelated config: %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "candidate.db")); !os.IsNotExist(err) {
		t.Fatalf("projection created database: %v", err)
	}
}

func TestDisabledAutomaticConsumersIgnoreLegacyEnvironmentAndCreateNoStore(t *testing.T) {
	root := t.TempDir()
	_, ctx := selectedConfig(t, "[storage]\npath = \""+filepath.ToSlash(filepath.Join(root, "store.db"))+"\"\n[integration]\ndisabled = true\n")
	t.Setenv("MNEMOS_DISABLED", "")
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"hook", func() error { return runHook(ctx, []string{"pre-tool"}) }},
		{"prewarm", func() error { return runPrewarm(ctx, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := tc.run(); err != nil {
					t.Fatal(err)
				}
			})
			if out != "" {
				t.Fatalf("disabled %s produced output: %q", tc.name, out)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "store.db")); !os.IsNotExist(err) {
		t.Fatalf("disabled automatic integration created store: %v", err)
	}
}

func TestVaultDisabledCommandsHaveNoOutputOrStorageEffectsAndStatusWorks(t *testing.T) {
	root := t.TempDir()
	vaultPath := filepath.Join(root, "would-be-vault")
	dbPath := filepath.Join(root, "would-be-store.db")
	_, ctx := selectedConfig(t, "[storage]\npath = \""+filepath.ToSlash(dbPath)+"\"\n[vault]\npath = \""+filepath.ToSlash(vaultPath)+"\"\nenabled = false\n")
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"export", func() error { return runVaultExport(ctx, []string{"--out", vaultPath}) }},
		{"watch", func() error { return runVaultWatch(ctx, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := tc.run(); err == nil || !strings.Contains(err.Error(), "disabled") {
					t.Fatalf("disabled %s error = %v", tc.name, err)
				}
			})
			if out != "" {
				t.Fatalf("disabled %s produced output: %q", tc.name, out)
			}
		})
	}
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Fatalf("disabled vault command created export: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("disabled vault command opened storage: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runVaultStatus(ctx, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, filepath.ToSlash(vaultPath)) || !strings.Contains(out, "last_exported_at: never") || !strings.Contains(out, "watcher: stopped") {
		t.Fatalf("vault status should report on-disk provenance even when disabled: %s", out)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("vault status opened storage: %v", err)
	}
}

func TestConfiguredSearchAndContextLimitsApplyWhenInputsAreOmitted(t *testing.T) {
	homeWithConfig(t, "[search]\ndefault_limit = 1\nmax_context_tokens = 100\n")
	d, err := loadDeps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	for i := 0; i < 3; i++ {
		if _, err := d.mem.Save(context.Background(), memory.SaveInput{AgentID: "default", Type: memory.TypeSemantic, Title: "needle fact", Content: strings.Repeat("needle detail ", 80) + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	defaultHits, err := d.mem.Search(context.Background(), memory.SearchInput{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	explicitHits, err := d.mem.Search(context.Background(), memory.SearchInput{Query: "needle", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultHits) != 1 || len(explicitHits) != 2 {
		t.Fatalf("search limits: omitted=%d explicit=%d, want 1 and 2", len(defaultHits), len(explicitHits))
	}
	defaultBlock, err := d.mem.Context(context.Background(), memory.ContextInput{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	explicitBlock, err := d.mem.Context(context.Background(), memory.ContextInput{Query: "needle", MaxTokens: 300})
	if err != nil {
		t.Fatal(err)
	}
	if defaultBlock.TokenEstimate > 100 || explicitBlock.TokenEstimate <= defaultBlock.TokenEstimate {
		t.Fatalf("context budgets: omitted=%d explicit=%d, want <=100 and larger", defaultBlock.TokenEstimate, explicitBlock.TokenEstimate)
	}
}

func TestConfigSelectorUsesSameRelativeStoreFromDifferentWorkingDirectories(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config folder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "selected config.toml")
	if err := os.WriteFile(path, []byte("[storage]\npath = \"relative/store.db\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := config.ContextWithPath(context.Background(), path)
	firstDir, secondDir := filepath.Join(root, "one"), filepath.Join(root, "two")
	for _, dir := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(firstDir)
	first, err := loadOperationConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(secondDir)
	second, err := loadOperationConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configDir, "relative", "store.db")
	if first.Storage.Path != want || second.Storage.Path != want {
		t.Fatalf("resolved storage paths differ by cwd: %q, %q; want %q", first.Storage.Path, second.Storage.Path, want)
	}
}
