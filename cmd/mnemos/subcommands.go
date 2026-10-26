package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/polyxmedia/mnemos/internal/vault"
)

// runDream executes dream-cycle consolidation. With --watch, runs as a
// daemon on the configured interval; otherwise performs a single pass
// and prints the journal.
func runDream(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("dream")
	watch := fs.Bool("watch", false, "run continuously on the configured interval")
	interval := fs.Duration("interval", 0, "override [dream].interval (Go duration: 6h, 30m, ...)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	svc, err := newMaintenanceRuntime(d.cfg, d.db, d.mem, d.skl, d.rum,
		d.db.Sessions(), d.db.Injections(), slog.Default())
	if err != nil {
		return err
	}

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
		return svc.Watch(ctx, every, true)
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
	const usage = "usage: mnemos vault <export|watch|status>"
	if err := treeHelp(args, usage); err != nil {
		return err
	}
	if len(args) == 0 {
		return usageErrorf("%s", usage)
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
		return usageErrorf("unknown vault subcommand: %s", sub)
	}
}

func runVaultWatch(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("vault watch")
	interval := fs.Duration("interval", 0, "override [vault].watch_interval")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
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
		Root: root, StorePath: d.cfg.Storage.Path, Obs: d.db.Observations(),
		Sessions: d.db.Sessions(), Skills: d.db.Skills(),
	})
	fmt.Fprintf(os.Stderr, "vault watch: %s every %s (ctrl-c to stop)\n", root, every)
	return ex.Watch(ctx, every)
}

func runVaultExport(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("vault export")
	out := fs.String("out", "", "override vault output directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
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
		Root: root, StorePath: d.cfg.Storage.Path, Obs: d.db.Observations(),
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

// runVaultStatus projects what is actually on disk. It deliberately does not
// call loadDeps: opening the store through storage.Open would create or
// migrate it, and status must stay a read-only projection.
func runVaultStatus(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("vault status")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		return err
	}
	interval, err := vault.ParseInterval(cfg.Vault.WatchInterval)
	if err != nil || interval <= 0 {
		interval = 5 * time.Minute
	}
	status := vault.InspectStatus(ctx, vault.StatusOptions{
		Root:      cfg.Vault.Path,
		StorePath: cfg.Storage.Path,
		Interval:  interval,
	})
	fmt.Printf("vault path: %s\n", status.Root)
	if !status.DirExists {
		fmt.Println("directory:  missing")
	}
	switch {
	case status.ManifestError != "":
		fmt.Printf("last_exported_at: unknown (%s)\n", status.ManifestError)
	case status.ManifestSeen:
		fmt.Printf("last_exported_at: %s\n", status.LastExportedAt.UTC().Format(time.RFC3339))
	default:
		fmt.Println("last_exported_at: never")
	}
	if status.PendingKnown {
		fmt.Printf("pending %d\n", status.Pending)
	} else {
		fmt.Println("pending: unknown")
	}
	switch {
	case status.WatcherRunning:
		fmt.Println("watcher: running")
	case status.HeartbeatSeen && !status.HeartbeatAt.IsZero():
		fmt.Printf("watcher: stopped (last heartbeat %s)\n", status.HeartbeatAt.UTC().Format(time.RFC3339))
	default:
		fmt.Println("watcher: stopped")
	}
	return nil
}

// runEmbed handles the `embed` subcommand tree: status | backfill.
func runEmbed(ctx context.Context, args []string) error {
	const usage = "usage: mnemos embed <status|backfill>"
	if err := treeHelp(args, usage); err != nil {
		return err
	}
	if len(args) == 0 {
		return usageErrorf("%s", usage)
	}
	sub := args[0]
	switch sub {
	case "status":
		fs := newCommandFlagSet("embed status")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usageErrorf("unexpected positional arguments")
		}
		return runEmbedStatus(ctx)
	case "backfill":
		fs := newCommandFlagSet("embed backfill")
		rebuild := fs.Bool("rebuild-incompatible", false, "also rebuild vectors with a different stored model or dimension")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usageErrorf("unexpected positional arguments")
		}
		return runEmbedBackfillOptions(ctx, embeddingBackfillOptions{RebuildIncompatible: *rebuild})
	default:
		return usageErrorf("unknown embed subcommand: %s", sub)
	}
}

func runEmbedStatus(ctx context.Context) error {
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()
	observations, err := d.db.Observations().Export(ctx)
	if err != nil {
		return fmt.Errorf("embed status: %w", err)
	}
	coverage := measureEmbeddingCoverage(observations, d.embedder.Model(), d.embedder.Dimension(), time.Now().UTC())
	fmt.Printf("provider: %s\n", d.cfg.Embedding.Provider)
	fmt.Printf("model:    %s\n", d.cfg.Embedding.Model)
	fmt.Printf("enabled:  %v\n", d.mem.HybridEnabled())
	fmt.Printf("embedded %d/%d\n", coverage.Embedded, coverage.Eligible)
	if len(coverage.Models) > 0 {
		fmt.Println("embedding models:")
		for _, usage := range coverage.Models {
			if usage.Mismatch {
				fmt.Printf("  %-24s %d  (dimension %d != active %d)\n",
					usage.Model, usage.Count, usage.Dimension, d.embedder.Dimension())
				continue
			}
			fmt.Printf("  %-24s %d\n", usage.Model, usage.Count)
		}
	}
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
	// The skip breakdown and the remainder come from one post-run snapshot.
	// They are best-effort: a cancelled context or a store that cannot be
	// re-read leaves the work counts intact rather than failing the run.
	if observations, exportErr := d.db.Observations().Export(ctx); exportErr == nil {
		report.Skipped, report.Remaining = embeddingBackfillGap(
			observations, d.embedder.Model(), d.embedder.Dimension(),
			options.RebuildIncompatible, time.Now().UTC())
	}
	if ctx.Err() != nil {
		fmt.Printf("embedding backfill interrupted: attempted=%d succeeded=%d failed=%d skipped=%s remaining=%d\n",
			report.Attempted, report.Succeeded, report.Failed, report.Skipped, report.Remaining)
		return fmt.Errorf("embedding backfill: %w", ctx.Err())
	}
	fmt.Printf("backfilled %d embeddings (attempted=%d succeeded=%d failed=%d skipped=%s remaining=%d)\n",
		report.Succeeded, report.Attempted, report.Succeeded, report.Failed, report.Skipped, report.Remaining)
	return err
}
