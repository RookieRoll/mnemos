package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

// Parse and route before general doctor's configuration creation, writable
// storage initialization or client checks. Even malformed flags have no effects.
func doctorWorkspaceMode(ctx context.Context, args []string, output io.Writer) (bool, error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaces := fs.Bool("workspaces", false, "diagnose recorded workspace identities without changing state")
	jsonOutput := fs.Bool("json", false, "render workspace diagnosis as JSON")
	if err := fs.Parse(args); err != nil {
		return true, fmt.Errorf("doctor: parse flags: %w", err)
	}
	if fs.NArg() != 0 {
		return true, fmt.Errorf("doctor: unexpected positional arguments")
	}
	if !*workspaces {
		if *jsonOutput {
			return true, fmt.Errorf("doctor: --json requires --workspaces")
		}
		return false, nil
	}
	cfg, err := config.LoadReadOnly(config.PathFromContext(ctx))
	if err != nil && !config.HasExplicitPath(ctx) && errors.Is(err, os.ErrNotExist) {
		cfg, err = config.Default(), nil
	}
	if err != nil {
		return true, fmt.Errorf("doctor: read-only config: %w", err)
	}
	budget := cfg.Diagnostics.Workspaces
	timeout, err := time.ParseDuration(budget.Timeout)
	if err != nil {
		return true, fmt.Errorf("doctor: workspace timeout: %w", err)
	}
	service, err := workspacediagnostics.NewService(workspacediagnostics.Config{
		Reader: storage.NewWorkspaceDiagnosticsReader(cfg.Storage.Path, budget.MaxCandidates), Probe: workspacediagnostics.LocalProbe{},
		MaxRoots: budget.MaxRoots, MaxCandidates: budget.MaxCandidates, Timeout: timeout,
	})
	if err != nil {
		return true, fmt.Errorf("doctor: workspace diagnostics: %w", err)
	}
	report, err := service.Diagnose(ctx)
	if err != nil {
		return true, fmt.Errorf("doctor: %w", err)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return true, fmt.Errorf("doctor: encode workspace report: %w", err)
		}
		return true, nil
	}
	return true, renderWorkspaceReport(output, report)
}

func renderWorkspaceReport(w io.Writer, r workspacediagnostics.Report) error {
	if _, err := fmt.Fprintf(w, "Workspace diagnostics: %s (read-only, advisory)\nRoots: %d/%d; compared pairs: %d/%d; candidates: %d\n",
		r.Status, r.ScannedRoots, r.TotalRoots, r.ComparedPairs, r.PossiblePairs, len(r.Candidates)); err != nil {
		return fmt.Errorf("doctor: render workspace report: %w", err)
	}
	if r.Partial {
		if _, err := fmt.Fprintf(w, "Incomplete coverage: %s; omitted comparisons are unknown.\n", strings.Join(r.Reasons, ", ")); err != nil {
			return fmt.Errorf("doctor: render incomplete coverage: %w", err)
		}
	}
	for _, root := range r.Roots {
		if _, err := fmt.Fprintf(w, "- %q [%s]: %s/%s; sessions=%d open=%d checkpoints=%d clears=%d\n",
			root.Root, root.Key, root.Path.Status, root.Path.Git, root.Counts.Sessions,
			root.Counts.OpenSessions, root.Counts.Checkpoints, root.Counts.ClearMarkers); err != nil {
			return fmt.Errorf("doctor: render root: %w", err)
		}
	}
	for _, c := range r.Candidates {
		if _, err := fmt.Fprintf(w, "- %s: %q [%s] <-> %q [%s]\n  evidence: %s\n  blockers: %s; retry overlaps=%d (known=%t); merge safety=%s\n",
			c.Classification, c.Left.Root, c.Left.Key, c.Right.Root, c.Right.Key,
			strings.Join(c.Evidence, ", "), strings.Join(c.Blockers, ", "), c.RetryOverlap, c.RetryOverlapKnown, c.MergeSafety); err != nil {
			return fmt.Errorf("doctor: render candidate: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w, "No workspace identity was registered or merged. Ordinary agent/project memory was not reclassified."); err != nil {
		return fmt.Errorf("doctor: render report footer: %w", err)
	}
	return nil
}
