package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/installer"
)

// parseInvocation handles only selectors before the command. Command-local
// selectors retain their own parsers and are checked against this context.
func parseInvocation(ctx context.Context, args []string) (context.Context, string, []string, error) {
	for len(args) > 0 && (args[0] == "--config" || strings.HasPrefix(args[0], "--config=")) {
		var path string
		if args[0] == "--config" {
			if len(args) < 2 {
				return ctx, "", nil, errors.New("--config requires a file path")
			}
			path, args = args[1], args[2:]
		} else {
			path, args = strings.TrimPrefix(args[0], "--config="), args[1:]
		}
		selected, err := selectConfigPath(ctx, path, true)
		if err != nil {
			return ctx, "", nil, err
		}
		ctx = config.ContextWithPath(ctx, selected)
	}
	if len(args) == 0 {
		return ctx, "", nil, errors.New("expected a command")
	}
	return ctx, args[0], args[1:], nil
}

func normalizeConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("config path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve config home: %w", err)
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], "/\\"))
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return filepath.Clean(path), nil
}

func selectConfigPath(ctx context.Context, path string, explicit bool) (string, error) {
	if !explicit {
		path = config.PathFromContext(ctx)
	}
	selected, err := normalizeConfigPath(path)
	if err != nil {
		return "", err
	}
	if explicit && config.HasExplicitPath(ctx) {
		previous, err := normalizeConfigPath(config.PathFromContext(ctx))
		if err != nil {
			return "", err
		}
		equal := previous == selected
		if runtime.GOOS == "windows" {
			equal = strings.EqualFold(previous, selected)
		}
		if !equal {
			return "", errors.New("conflicting configuration selectors")
		}
	}
	if explicit || config.HasExplicitPath(ctx) {
		info, err := os.Stat(selected)
		if err != nil {
			return "", fmt.Errorf("selected config: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("selected config is not a regular file")
		}
	}
	return selected, nil
}

func loadOperationConfig(ctx context.Context) (config.Config, error) {
	path, err := selectConfigPath(ctx, "", false)
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(path)
}

// readOperationConfig never creates a file or initializes service dependencies.
func readOperationConfig(ctx context.Context) (config.Config, error) {
	path, err := selectConfigPath(ctx, "", false)
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.LoadReadOnly(path)
	if errors.Is(err, os.ErrNotExist) && !config.HasExplicitPath(ctx) {
		return config.Default(), nil
	}
	return cfg, err
}

func installerOptions(cfg config.Config) installer.Options {
	return installer.Options{
		ClaudeConfigDir:         cfg.Clients.ClaudeConfigDir,
		PiAgentDir:              cfg.Clients.PiAgentDir,
		ClaudeDesktopConfigPath: cfg.Clients.ClaudeDesktopConfigPath,
	}
}

func configuredCommand(ctx context.Context, executable string) (string, error) {
	prefix := quoteCommandArg(executable)
	if runtime.GOOS == "windows" && strings.ContainsAny(executable, "%!$`") {
		return "", errors.New("executable path contains a character that cannot be represented safely in a Windows hook command")
	}
	if config.HasExplicitPath(ctx) {
		path := config.PathFromContext(ctx)
		if runtime.GOOS == "windows" && strings.ContainsAny(path, "%!$`") {
			return "", errors.New("configuration path contains a character that cannot be represented safely in a Windows hook command")
		}
		prefix += " --config " + quoteCommandArg(path)
	}
	return prefix, nil
}

func quoteCommandArg(value string) string {
	if runtime.GOOS == "windows" {
		return "\"" + strings.ReplaceAll(value, "\"", "\\\"") + "\""
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
