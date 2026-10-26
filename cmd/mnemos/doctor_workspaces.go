package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver; the stale scan is read-only

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

// Parse and route before general doctor's configuration creation, writable
// storage initialization or client checks. Even malformed flags have no effects.
func doctorWorkspaceMode(ctx context.Context, args []string, output io.Writer) (bool, error) {
	fs := newCommandFlagSet("doctor")
	workspaces := fs.Bool("workspaces", false, "diagnose recorded workspace identities without changing state")
	jsonOutput := fs.Bool("json", false, "render workspace diagnosis as JSON")
	if err := parseFlags(fs, args); err != nil {
		return true, err
	}
	if fs.NArg() != 0 {
		return true, usageErrorf("doctor: unexpected positional arguments")
	}
	if !*workspaces {
		if *jsonOutput {
			return true, usageErrorf("doctor: --json requires --workspaces")
		}
		// Ordinary doctor reports populated sibling databases as advisory
		// warnings before its own checks. The scan is read-only and never
		// changes doctor's exit status.
		if err := reportStaleDatabases(ctx, output); err != nil {
			return true, err
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

// staleDatabaseFinding is one populated candidate database that is not the
// configured store.
type staleDatabaseFinding struct {
	// Path is the candidate database file.
	Path string
	// LastWrite is the later modification time of the database and its -wal.
	LastWrite time.Time
}

// unreadableDatabaseFinding is one candidate that could not be opened
// read-only. It is reported and skipped; the remaining candidates still scan.
type unreadableDatabaseFinding struct {
	// Path is the candidate database file.
	Path string
	// Err is the read-only open or query failure.
	Err string
}

// staleScanLine is one rendered finding, kept with its path so stale and
// unreadable lines can share one deterministic path order.
type staleScanLine struct {
	path string
	text string
}

// staleDatabaseScan is the advisory result of scanning top-level directories
// for populated databases other than the configured store.
type staleDatabaseScan struct {
	// Stale holds populated candidates ordered by path.
	Stale []staleDatabaseFinding
	// Unreadable holds unreadable candidates ordered by path.
	Unreadable []unreadableDatabaseFinding
}

// reportStaleDatabases prints the stale-database scan for ordinary doctor. It
// is deliberately silent when the configuration cannot be read: general
// doctor reports that failure itself, and this scan must never create, migrate
// or repair a store. Findings are warnings and never become an error.
func reportStaleDatabases(ctx context.Context, output io.Writer) error {
	configPath := config.PathFromContext(ctx)
	cfg, err := config.LoadReadOnly(configPath)
	if err != nil {
		if config.HasExplicitPath(ctx) || !errors.Is(err, os.ErrNotExist) {
			return nil
		}
		cfg = config.Default()
	}
	roots := staleScanRoots(configPath)
	if len(roots) == 0 {
		return nil
	}
	scan := scanStaleDatabases(ctx, cfg.Storage.Path, roots)
	if len(scan.Stale) == 0 && len(scan.Unreadable) == 0 {
		return nil
	}
	return renderStaleDatabaseScan(output, scan)
}

// staleScanRoots is the bounded scan surface: the default data directory and
// the selected config file's directory, top level only. Roots are resolved to
// absolute cleaned paths and deduplicated, so a config living in ~/.mnemos is
// examined once.
func staleScanRoots(configPath string) []string {
	var raw []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		raw = append(raw, filepath.Join(home, ".mnemos"))
	}
	if configPath != "" {
		raw = append(raw, filepath.Dir(configPath))
	}
	seen := make(map[string]bool, len(raw))
	roots := make([]string, 0, len(raw))
	for _, root := range raw {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		key := diagnosticPathKey(abs)
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, abs)
	}
	sort.Strings(roots)
	return roots
}

// scanStaleDatabases enumerates top-level *.db candidates under roots and
// probes each once for observations. The configured store is excluded by
// absolute-path comparison. Nothing is written, renamed, migrated or removed.
func scanStaleDatabases(ctx context.Context, configuredPath string, roots []string) staleDatabaseScan {
	var scan staleDatabaseScan
	configuredKey := diagnosticPathKey(configuredPath)
	seen := make(map[string]bool)
	var candidates []string
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		entries, err := os.ReadDir(abs)
		if err != nil {
			// Missing or unreadable directories are not findings.
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
				continue
			}
			path := filepath.Join(abs, entry.Name())
			key := diagnosticPathKey(path)
			if key == configuredKey || seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, path)
		}
	}
	sort.Strings(candidates)
	for _, path := range candidates {
		lastWrite, err := staleDatabaseLastWrite(path)
		if err != nil {
			// The file vanished between ReadDir and Stat; it is no longer a
			// candidate and there is nothing to report.
			continue
		}
		populated, err := staleDatabasePopulated(ctx, path)
		if err != nil {
			scan.Unreadable = append(scan.Unreadable, unreadableDatabaseFinding{Path: path, Err: err.Error()})
			continue
		}
		if populated {
			scan.Stale = append(scan.Stale, staleDatabaseFinding{Path: path, LastWrite: lastWrite})
		}
	}
	return scan
}

// staleDatabaseLastWrite is the later modification time of the database file
// and its -wal file, read with os.Stat only: no open, no lock, no write. The
// -wal mtime counts because committed rows can live there until checkpoint.
func staleDatabaseLastWrite(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat candidate: %w", err)
	}
	if !info.Mode().IsRegular() {
		return time.Time{}, fmt.Errorf("candidate is not a regular file")
	}
	lastWrite := info.ModTime()
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.Mode().IsRegular() && wal.ModTime().After(lastWrite) {
		lastWrite = wal.ModTime()
	}
	return lastWrite, nil
}

// staleDatabasePopulated reports whether a candidate holds at least one
// observation. A non-empty -wal file is accepted as evidence of committed data
// without a read-only open, the documented fallback for WAL-backed candidates
// that cannot be opened read-only safely; every other open or query failure is
// returned as unreadable. A database without an observations table holds no
// mnemos rows and is silent.
func staleDatabasePopulated(ctx context.Context, path string) (bool, error) {
	if populatedWALFile(path) {
		return true, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := sql.Open("sqlite", staleDatabaseDSN(path))
	if err != nil {
		return false, fmt.Errorf("open read-only: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var hasTable bool
	if err := db.QueryRowContext(probeCtx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='observations')`).Scan(&hasTable); err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	if !hasTable {
		return false, nil
	}
	var one int
	err = db.QueryRowContext(probeCtx, `SELECT 1 FROM observations LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe observations: %w", err)
	}
	return true, nil
}

// populatedWALFile reports whether a non-empty -wal file sits next to path.
func populatedWALFile(path string) bool {
	info, err := os.Stat(path + "-wal")
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// staleDatabaseDSN is a SQLite URI that cannot write the candidate. immutable=1
// guarantees no locks and no -shm/-wal creation; a candidate with committed WAL
// content is decided from file state before this URI is ever used.
func staleDatabaseDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uriPath := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{
		"mode":      {"ro"},
		"immutable": {"1"},
		"_pragma":   {"query_only(1)", "temp_store(MEMORY)"},
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// diagnosticPathKey normalizes a path for identity comparisons. Windows paths
// compare case-insensitively; the display path keeps its original casing.
func diagnosticPathKey(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// renderStaleDatabaseScan writes one deterministic, path-sorted line per
// finding. Paths under the home directory are abbreviated to "~" for reading,
// matching how users name the default store.
func renderStaleDatabaseScan(w io.Writer, scan staleDatabaseScan) error {
	lines := make([]staleScanLine, 0, len(scan.Stale)+len(scan.Unreadable))
	for _, finding := range scan.Stale {
		lines = append(lines, staleScanLine{
			path: finding.Path,
			text: fmt.Sprintf("stale DB at %s (last write %s)",
				displayDiagnosticPath(finding.Path), finding.LastWrite.UTC().Format(time.RFC3339)),
		})
	}
	for _, finding := range scan.Unreadable {
		lines = append(lines, staleScanLine{
			path: finding.Path,
			text: fmt.Sprintf("unreadable DB at %s (%s)", displayDiagnosticPath(finding.Path), finding.Err),
		})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].path < lines[j].path })
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line.text); err != nil {
			return fmt.Errorf("doctor: render stale database scan: %w", err)
		}
	}
	return nil
}

// displayDiagnosticPath abbreviates the home directory to "~".
func displayDiagnosticPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + path[len(prefix):]
	}
	return path
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
