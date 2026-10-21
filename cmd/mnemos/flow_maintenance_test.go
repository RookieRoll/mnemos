package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestSessionStartRunsMaintenanceOnlyWhenBothGatesAllowIt(t *testing.T) {
	tests := []struct {
		name            string
		maintenanceOn   bool
		integrationOff  bool
		wantLastSuccess bool
	}{
		{name: "enabled", maintenanceOn: true, integrationOff: true, wantLastSuccess: true},
		{name: "maintenance disabled", maintenanceOn: false, integrationOff: true},
		{name: "integration disabled", maintenanceOn: true, integrationOff: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "flow-maintenance.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			cfg := config.Default()
			cfg.Maintenance.Enabled = tc.maintenanceOn
			cfg.Integration.Disabled = !tc.integrationOff
			mem := memory.NewService(memory.Config{Store: db.Observations()})
			skl := skills.NewService(skills.Config{Store: db.Skills()})
			coordinator, err := newMaintenanceRuntime(cfg, db, mem, skl, nil, db.Sessions(), db.Injections(), slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			flow := continuityflow.New(continuityflow.Config{
				LegacySessions: session.NewService(session.Config{Store: db.Sessions()}),
				Maintenance:    coordinator,
			})
			out, err := flow.Start(ctx, continuityflow.TaskSessionInput{Project: "fixture"})
			if err != nil || out.Receipt.LegacySession == nil {
				t.Fatalf("session-start receipt=%#v err=%v", out, err)
			}

			var lastSuccess any
			if err := db.SQL().QueryRowContext(ctx, `SELECT last_success FROM maintenance_coordinator WHERE singleton = 1`).Scan(&lastSuccess); err != nil {
				t.Fatal(err)
			}
			if got := lastSuccess != nil; got != tc.wantLastSuccess {
				t.Fatalf("last_success present=%t, want %t", got, tc.wantLastSuccess)
			}
		})
	}
}
