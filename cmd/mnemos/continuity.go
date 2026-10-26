package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/embedding"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func runContinuity(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "capabilities" {
		// Deliberately precedes config.Load, storage.Open and provider selection.
		return writeContinuityJSON(map[string]any{"protocol_version": 1, "supported": true})
	}
	if len(args) == 0 {
		return continuityUsage(fmt.Errorf("%w: expected capabilities, open, checkpoint or context", continuity.ErrValidation))
	}
	if args[0] != "open" && args[0] != "checkpoint" && args[0] != "context" {
		return continuityUsage(fmt.Errorf("%w: unknown continuity command", continuity.ErrUnsupported))
	}
	fs := newCommandFlagSet("continuity " + args[0])
	payload := fs.String("payload", "", "explicit continuity v1 JSON request (maximum 32 KiB)")
	configPath := fs.String("config", "", "path to config file")
	if err := parseFlags(fs, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return continuityUsage(fmt.Errorf("%w: invalid flags: %v", continuity.ErrValidation, err))
	}
	if fs.NArg() != 0 {
		return continuityUsage(fmt.Errorf("%w: unexpected arguments", continuity.ErrValidation))
	}
	var open continuity.OpenInput
	var checkpoint continuity.CheckpointInput
	var read continuity.ContextInput
	var target any
	switch args[0] {
	case "open":
		target = &open
	case "checkpoint":
		target = &checkpoint
	case "context":
		target = &read
	}
	if err := continuity.DecodeJSON([]byte(*payload), target); err != nil {
		// A payload the v1 decoder rejects is the caller's mistake, not a store
		// failure: answer in the envelope and exit with the usage status.
		return continuityUsage(err)
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
	path, err := selectConfigPath(ctx, *configPath, explicit)
	if err != nil {
		return continuityCLIError(err)
	}
	if explicit {
		ctx = config.ContextWithPath(ctx, path)
	}
	cfg, err := loadOperationConfig(ctx)
	if err != nil {
		return continuityCLIError(err)
	}
	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return continuityCLIError(err)
	}
	defer db.Close()
	// A progress-only call must not probe or initialize an embedding provider.
	core := continuity.New(continuity.Config{Store: db.Continuity()})
	injections := injection.NewLogger(db.Injections(), nil)
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills(),
		Touches: db.Touches(), Injections: injections, MaxTokens: cfg.Search.PrewarmMaxTokens,
	})
	legacySessions := session.NewService(session.Config{Store: db.Sessions()})
	flowConfig := continuityflow.Config{
		Core: core, LegacySessions: legacySessions, Composer: pw,
		PrewarmMaxTokens: cfg.Search.PrewarmMaxTokens,
	}
	if args[0] == "open" {
		skl := skills.NewService(skills.Config{Store: db.Skills()})
		mem := memory.NewService(memoryServiceConfig(cfg.Search, db.Observations(), embedding.NewNoop(), injections))
		rum := newRumination(cfg, db, skl)
		if maintenance, buildErr := newMaintenanceRuntime(cfg, db, mem, skl, rum, db.Sessions(), db.Injections(), slog.Default()); buildErr == nil {
			flowConfig.Maintenance = maintenance
		}
	}
	flow := continuityflow.New(flowConfig)
	var out any
	switch args[0] {
	case "open":
		out, err = flow.Open(ctx, open)
	case "checkpoint":
		out, err = flow.Checkpoint(ctx, checkpoint)
	case "context":
		out, err = flow.Context(ctx, read)
	}
	if err != nil {
		return continuityCLIError(err)
	}
	return writeContinuityJSON(out)
}

func writeContinuityJSON(out any) error {
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("continuity: encode result: %w", err)
	}
	_, err = os.Stdout.Write(buf)
	if err != nil {
		return fmt.Errorf("continuity: write result: %w", err)
	}
	return nil
}

// continuityUsage writes the continuity v1 error envelope and classifies the
// outcome as a usage error. dispatch then exits 2 without printing prose on
// top of the machine-readable body, the same way maintenanceJSONError keeps a
// written response clean for exit 1.
func continuityUsage(err error) error {
	return quietUsageError(continuityCLIError(err))
}

func continuityCLIError(err error) error {
	body := map[string]any{"protocol_version": 1, "code": continuity.ErrorCode(err), "error": continuityPublicError(err)}
	var detail *continuity.Error
	if errors.As(err, &detail) && detail.CurrentRevision != nil {
		body["current_revision"] = *detail.CurrentRevision
	}
	_ = writeContinuityJSON(body)
	return fmt.Errorf("continuity: %s", continuity.ErrorCode(err))
}

func continuityPublicError(err error) string {
	var detail *continuity.Error
	if errors.As(err, &detail) {
		return detail.Message
	}
	// Storage and decoder errors can contain submitted text or local paths.
	// Public failures report categories; detailed bodies are never logged.
	return "continuity request failed: " + continuity.ErrorCode(err)
}
