package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func runContinuity(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "capabilities" {
		// Deliberately precedes config.Load, storage.Open and provider selection.
		return writeContinuityJSON(map[string]any{"protocol_version": 1, "supported": true})
	}
	if len(args) == 0 {
		return continuityCLIError(fmt.Errorf("%w: expected capabilities, open, checkpoint or context", continuity.ErrValidation))
	}
	if args[0] != "open" && args[0] != "checkpoint" && args[0] != "context" {
		return continuityCLIError(fmt.Errorf("%w: unknown continuity command", continuity.ErrUnsupported))
	}
	fs := flag.NewFlagSet("continuity "+args[0], flag.ContinueOnError)
	payload := fs.String("payload", "", "explicit continuity v1 JSON request (maximum 32 KiB)")
	configPath := fs.String("config", config.DefaultPath(), "path to config file")
	if err := fs.Parse(args[1:]); err != nil {
		return continuityCLIError(fmt.Errorf("%w: invalid flags", continuity.ErrValidation))
	}
	if fs.NArg() != 0 {
		return continuityCLIError(fmt.Errorf("%w: unexpected arguments", continuity.ErrValidation))
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
		return continuityCLIError(err)
	}
	cfg, err := config.Load(*configPath)
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
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills(),
		Touches: db.Touches(), Injections: injection.NewLogger(db.Injections(), nil), MaxTokens: 500,
	})
	flow := continuityflow.New(continuityflow.Config{Core: core, Composer: pw})
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
