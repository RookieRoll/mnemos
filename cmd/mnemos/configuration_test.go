package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

func TestParseInvocationRejectsConflictingConfigSelectors(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := config.ContextWithPath(context.Background(), first)
	if _, _, _, err := parseInvocation(ctx, []string{"--config", second, "stats"}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting selectors returned %v", err)
	}
}

func TestRunConfigPiProjectionDoesNotCreateConfigOrStore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing", "selected config.toml")
	ctx := config.ContextWithPath(context.Background(), path)
	if err := runConfig(ctx, []string{"--client", "pi"}); err == nil {
		t.Fatal("explicit missing config should fail")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("projection created config parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "mnemos.db")); !os.IsNotExist(err) {
		t.Fatalf("projection created storage: %v", err)
	}
}

func TestQuoteCommandArgProtectsShellSyntax(t *testing.T) {
	value := `D:\Study\mnemos O'Neil & $HOME\mnemos.exe`
	got := quoteCommandArg(value)
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Fatalf("Windows path is not always quoted: %q", got)
		}
		if !strings.Contains(got, `\`) {
			t.Fatalf("Windows path lost backslashes: %q", got)
		}
		return
	}
	if got != `'D:\Study\mnemos O'\''Neil & $HOME\mnemos.exe'` {
		t.Fatalf("POSIX quoting = %q", got)
	}
}

func TestDefaultPathIgnoresFormerConfigEnvironment(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy.toml")
	t.Setenv("MNEMOS_CONFIG", legacy)
	if got := config.DefaultPath(); got == legacy {
		t.Fatalf("legacy environment changed default config path: %q", got)
	}
}
