package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/polyxmedia/mnemos/internal/dream"
	"github.com/polyxmedia/mnemos/internal/vault"
)

// runDream executes dream-cycle consolidation. With --watch, runs as a
// daemon on the configured interval; otherwise performs a single pass
// and prints the journal.
func runDream(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dream", flag.ContinueOnError)
	watch := fs.Bool("watch", false, "run continuously on the configured interval")
	interval := fs.Duration("interval", 0, "override [dream].interval (Go duration: 6h, 30m, ...)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	svc := dream.NewService(dream.Config{
		Memory:      d.mem,
		Store:       d.db.Observations(),
		Reader:      d.db.Observations(),
		Skills:      d.skl,
		Rumination:  d.rum,
		Sessions:    d.db.Sessions(),
		Injections:  d.db.Injections(),
		Logger:      slog.Default(),
		StaleDays:   d.cfg.Dream.StaleDays,
		DecayAmount: d.cfg.Dream.DecayAmount,
	})

	if *watch {
		every := *interval
		if every == 0 {
			parsed, err := time.ParseDuration(d.cfg.Dream.Interval)
			if err != nil && d.cfg.Dream.Interval != "" {
				return fmt.Errorf("parse [dream].interval: %w", err)
			}
			every = parsed
		}
		if every == 0 {
			every = 6 * time.Hour
		}
		fmt.Fprintf(os.Stderr, "dream watch: every %s (ctrl-c to stop)\n", every)
		return svc.Watch(ctx, every)
	}

	j, err := svc.Run(ctx, true)
	if err != nil {
		return fmt.Errorf("dream: %w", err)
	}
	fmt.Println(j.Summary())
	return nil
}

// runVault handles the `vault` subcommand tree: export | watch | status.
func runVault(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos vault <export|watch|status>")
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "export":
		return runVaultExport(ctx, rest)
	case "watch":
		return runVaultWatch(ctx, rest)
	case "status":
		return runVaultStatus(ctx, rest)
	default:
		return fmt.Errorf("unknown vault subcommand: %s", sub)
	}
}

func runVaultWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vault watch", flag.ContinueOnError)
	interval := fs.Duration("interval", 0, "override [vault].watch_interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Vault.Enabled {
		return fmt.Errorf("vault watch: disabled; set [vault].enabled = true")
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	root := d.cfg.Vault.Path
	if root == "" {
		return fmt.Errorf("no vault path configured; set [vault].path")
	}
	every := *interval
	if every == 0 {
		parsed, err := vault.ParseInterval(d.cfg.Vault.WatchInterval)
		if err != nil {
			return err
		}
		every = parsed
	}
	if every == 0 {
		every = 5 * time.Minute
	}

	ex := vault.NewExporter(vault.Config{
		Root: root, Obs: d.db.Observations(),
		Sessions: d.db.Sessions(), Skills: d.db.Skills(),
	})
	fmt.Fprintf(os.Stderr, "vault watch: %s every %s (ctrl-c to stop)\n", root, every)
	return ex.Watch(ctx, every)
}

func runVaultExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vault export", flag.ContinueOnError)
	out := fs.String("out", "", "override vault output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Vault.Enabled {
		return fmt.Errorf("vault export: disabled; set [vault].enabled = true")
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	root := d.cfg.Vault.Path
	if *out != "" {
		root = *out
	}
	if root == "" {
		return fmt.Errorf("no vault path configured; pass --out or set [vault].path")
	}

	ex := vault.NewExporter(vault.Config{
		Root: root, Obs: d.db.Observations(),
		Sessions: d.db.Sessions(), Skills: d.db.Skills(),
	})
	stats, err := ex.ExportAll(ctx)
	if err != nil {
		return fmt.Errorf("vault export: %w", err)
	}
	fmt.Printf("exported %d observations, %d sessions, %d skills, %d tag MOCs to %s\n",
		stats.Observations, stats.Sessions, stats.Skills, stats.Tags, root)
	return nil
}

func runVaultStatus(ctx context.Context, _ []string) error {
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("vault path: %s\n", cfg.Vault.Path)
	if _, err := os.Stat(cfg.Vault.Path); err != nil {
		fmt.Println("status:     not yet exported")
		return nil
	}
	fmt.Println("status:     exists")
	return nil
}

// runEmbed handles the `embed` subcommand tree: status | backfill.
func runEmbed(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mnemos embed <status|backfill>")
	}
	sub := args[0]
	switch sub {
	case "status":
		return runEmbedStatus(ctx)
	case "backfill":
		fs := flag.NewFlagSet("embed backfill", flag.ContinueOnError)
		rebuild := fs.Bool("rebuild-incompatible", false, "also rebuild vectors with a different stored model or dimension")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("embed backfill: unexpected positional arguments")
		}
		return runEmbedBackfillOptions(ctx, embeddingBackfillOptions{RebuildIncompatible: *rebuild})
	default:
		return fmt.Errorf("unknown embed subcommand: %s", sub)
	}
}

func runEmbedStatus(ctx context.Context) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()
	fmt.Printf("provider: %s\n", d.cfg.Embedding.Provider)
	fmt.Printf("model:    %s\n", d.cfg.Embedding.Model)
	fmt.Printf("enabled:  %v\n", d.mem.HybridEnabled())
	return nil
}

func runEmbedBackfill(ctx context.Context) error {
	return runEmbedBackfillOptions(ctx, embeddingBackfillOptions{})
}

func runEmbedBackfillOptions(ctx context.Context, options embeddingBackfillOptions) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	store, ok := d.db.Observations().(embeddingBackfillStore)
	if !ok {
		return fmt.Errorf("embedding backfill: store does not support bounded maintenance")
	}
	report, err := runEmbeddingBackfill(ctx, store, d.embedder, options)
	if ctx.Err() != nil {
		fmt.Printf("embedding backfill interrupted: attempted=%d succeeded=%d failed=%d\n", report.Attempted, report.Succeeded, report.Failed)
		return fmt.Errorf("embedding backfill: %w", ctx.Err())
	}
	fmt.Printf("backfilled %d embeddings (attempted=%d succeeded=%d failed=%d)\n", report.Succeeded, report.Attempted, report.Succeeded, report.Failed)
	return err
}
