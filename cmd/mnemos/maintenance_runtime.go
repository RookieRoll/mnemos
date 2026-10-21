package main

import (
	"fmt"
	"log/slog"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// newMaintenanceRuntime builds the shared dream runner and durable coordinator
// from dependencies already opened by the caller. Configuration defaults and
// validation have already been applied by config.Load.
func newMaintenanceRuntime(
	cfg config.Config,
	db *storage.DB,
	mem *memory.Service,
	skl *skills.Service,
	rum *rumination.Service,
	sessions session.Store,
	injections injection.Store,
	logger *slog.Logger,
) (*dream.Coordinator, error) {
	if db == nil || mem == nil || skl == nil {
		return nil, fmt.Errorf("maintenance runtime: database, memory, and skills are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	runner := dream.NewService(dream.Config{
		Memory: mem, Store: db.FencedObservations(), Reader: db.Observations(),
		Skills: skl, Rumination: rum, Sessions: sessions, Injections: injections,
		Logger: logger, StaleDays: cfg.Dream.StaleDays, DecayAmount: cfg.Dream.DecayAmount,
	})
	maintenanceCfg := cfg.Maintenance
	coordinator, err := dream.NewCoordinator(dream.CoordinatorConfig{
		Store: db.Maintenance(), Runner: runner,
		AutomaticEnabled: maintenanceCfg.Enabled && !cfg.Integration.Disabled,
		Interval:         maintenanceCfg.Interval, MaxPassDuration: maintenanceCfg.MaxPassDuration,
		LeaseDuration: maintenanceCfg.LeaseDuration, RenewInterval: maintenanceCfg.RenewInterval,
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("maintenance runtime: %w", err)
	}
	return coordinator, nil
}
