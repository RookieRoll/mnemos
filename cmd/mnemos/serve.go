package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/version"
)

// buildRumination is defined in commands.go as newRumination; serve
// re-uses that helper so the rumination wiring stays in one place.

// runServe starts the MCP stdio server (default) or the HTTP server when
// --http is passed. Logs go to stderr so they don't pollute the stdio
// JSON-RPC stream on stdout.
func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "start HTTP server on ADDR instead of stdio (e.g. :8080)")
	cfgPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return fmt.Errorf("serve: unexpected positional arguments")
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
	path, err := selectConfigPath(ctx, *cfgPath, explicit)
	if err != nil {
		return err
	}
	if explicit {
		ctx = config.ContextWithPath(ctx, path)
	}
	cfg, err := loadOperationConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer db.Close()

	embedder := selectEmbedder(ctx, cfg.Embedding)
	logger.Info("embedding", "provider", embedder.Model(), "dim", embedder.Dimension())

	injLog := injection.NewLogger(db.Injections(), nil)
	mem := memory.NewService(memoryServiceConfig(cfg.Search, db.Observations(), embedder, injLog))
	sess := session.NewService(session.Config{Store: db.Sessions()})
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := newRumination(cfg, db, skl)
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
		Rumination:   rum,
		Injections:   injLog,
		MaxTokens:    cfg.Search.PrewarmMaxTokens,
	})
	maintenance, err := newMaintenanceRuntime(cfg, db, mem, skl, rum, db.Sessions(), db.Injections(), logger)
	if err != nil {
		return fmt.Errorf("build maintenance runtime: %w", err)
	}
	continuitySvc := continuityflow.New(continuityflow.Config{
		Core: continuity.New(continuity.Config{Store: db.Continuity()}), LegacySessions: sess,
		Composer: pw, Maintenance: maintenance,
		PrewarmMaxTokens: cfg.Search.PrewarmMaxTokens,
	})

	addr := *httpAddr
	if addr != "" || cfg.Server.Transport == "http" {
		if addr == "" {
			addr = cfg.Server.HTTPAddr
		}
		httpSrv := api.NewServer(api.Config{
			Flow:     continuitySvc,
			Memory:   mem,
			Sessions: sess,
			Skills:   skl,
			Touches:  db.Touches(),
			Prewarm:  pw,
			APIKey:   cfg.Server.APIKey,
			Logger:   logger,
			StorageSize: func() (int64, error) {
				fi, err := os.Stat(cfg.Storage.Path)
				if err != nil {
					return 0, err
				}
				return fi.Size(), nil
			},
		})
		logger.Info("mnemos serve (http)", "version", version.Version, "addr", addr)
		return httpSrv.Serve(ctx, addr)
	}

	srv := mcp.NewServer(mcp.Config{
		Flow:       continuitySvc,
		Name:       "mnemos",
		Version:    version.Version,
		Profile:    cfg.MCP.Profile,
		Memory:     mem,
		Sessions:   sess,
		Skills:     skl,
		Touches:    db.Touches(),
		Prewarm:    pw,
		Rumination: rum,
		Logger:     logger,
		StorageSize: func() (int64, error) {
			fi, err := os.Stat(cfg.Storage.Path)
			if err != nil {
				return 0, err
			}
			return fi.Size(), nil
		},
		DefaultProject: serverProject(),
	})

	logger.Info("mnemos serve (stdio)", "version", version.Version, "db", cfg.Storage.Path)
	if err := srv.ServeStdio(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// serverProject names the project writes default to: the basename of the
// directory the server was started in. Claude Code and Codex start stdio
// servers in the project root; a client that starts them in / or the home
// directory has no project to offer, and writes stay project-less.
func serverProject() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	return projectFromDir(cwd, home)
}

// projectFromDir is serverProject's pure core.
func projectFromDir(dir, home string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir { // filesystem or volume root
		return ""
	}
	if home != "" && dir == filepath.Clean(home) {
		return ""
	}
	return filepath.Base(dir)
}
