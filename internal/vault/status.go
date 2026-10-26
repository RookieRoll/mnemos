package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver; status reads stores read-only
)

const (
	// ExportManifestFile is the vault-local record of the last successful
	// export. `mnemos vault status` reads it as the export watermark.
	ExportManifestFile = ".mnemos-vault-export.json"
	// WatchHeartbeatFile is the vault-local heartbeat published by
	// `mnemos vault watch`. It is informational, never a lock: a second
	// watcher overwrites it and status reflects the most recent heartbeat.
	WatchHeartbeatFile = ".mnemos-vault-watch.json"
)

// ExportManifest records when a vault export completed, how many observations
// it wrote and which store it read. It lives inside the vault directory so
// export provenance travels with the exported files.
type ExportManifest struct {
	// ExportedAt is the completion time in RFC 3339 UTC.
	ExportedAt time.Time `json:"exported_at"`
	// Observations is the number of observation files the export wrote.
	Observations int `json:"observations"`
	// Store is the database path the export read.
	Store string `json:"store"`
}

// WatchHeartbeat is the non-locking liveness record `mnemos vault watch`
// writes at start and on every tick.
type WatchHeartbeat struct {
	// PID is the process that published the heartbeat.
	PID int `json:"pid"`
	// StartedAt is when the watch loop started.
	StartedAt time.Time `json:"started_at"`
	// HeartbeatAt is the most recent tick (or start) time.
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

// WriteExportManifest atomically replaces the export manifest under root. The
// temp-file-plus-rename write means an interrupted export leaves the previous
// manifest, and therefore a truthful pending count, intact.
func WriteExportManifest(root string, manifest ExportManifest) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("vault: write export manifest: empty vault root")
	}
	manifest.ExportedAt = manifest.ExportedAt.UTC()
	if err := writeVaultJSONAtomic(filepath.Join(root, ExportManifestFile), manifest); err != nil {
		return fmt.Errorf("vault: write export manifest: %w", err)
	}
	return nil
}

// ReadExportManifest reads the export manifest under root. seen reports that
// the file exists; a missing manifest is (zero, false, nil). A present but
// unparsable manifest is (zero, true, err) so callers can distinguish "never
// exported" from "provenance unreadable".
func ReadExportManifest(root string) (ExportManifest, bool, error) {
	var manifest ExportManifest
	if strings.TrimSpace(root) == "" {
		return manifest, false, nil
	}
	path := filepath.Join(root, ExportManifestFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return manifest, false, nil
	}
	if err != nil {
		return manifest, false, fmt.Errorf("vault: read export manifest: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ExportManifest{}, true, fmt.Errorf("vault: parse export manifest %s: %w", path, err)
	}
	manifest.ExportedAt = manifest.ExportedAt.UTC()
	return manifest, true, nil
}

// WriteWatchHeartbeat atomically replaces the watch heartbeat under root.
func WriteWatchHeartbeat(root string, heartbeat WatchHeartbeat) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("vault: write watch heartbeat: empty vault root")
	}
	heartbeat.StartedAt = heartbeat.StartedAt.UTC()
	heartbeat.HeartbeatAt = heartbeat.HeartbeatAt.UTC()
	if err := writeVaultJSONAtomic(filepath.Join(root, WatchHeartbeatFile), heartbeat); err != nil {
		return fmt.Errorf("vault: write watch heartbeat: %w", err)
	}
	return nil
}

// ReadWatchHeartbeat reads the watch heartbeat under root. seen reports that
// the file exists; a missing heartbeat is (zero, false, nil).
func ReadWatchHeartbeat(root string) (WatchHeartbeat, bool, error) {
	var heartbeat WatchHeartbeat
	if strings.TrimSpace(root) == "" {
		return heartbeat, false, nil
	}
	path := filepath.Join(root, WatchHeartbeatFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return heartbeat, false, nil
	}
	if err != nil {
		return heartbeat, false, fmt.Errorf("vault: read watch heartbeat: %w", err)
	}
	if err := json.Unmarshal(data, &heartbeat); err != nil {
		return WatchHeartbeat{}, true, fmt.Errorf("vault: parse watch heartbeat %s: %w", path, err)
	}
	heartbeat.StartedAt = heartbeat.StartedAt.UTC()
	heartbeat.HeartbeatAt = heartbeat.HeartbeatAt.UTC()
	return heartbeat, true, nil
}

// RemoveWatchHeartbeat removes the heartbeat on clean shutdown. It is a no-op
// when a different process has since overwritten the file, so an exiting
// watcher never erases its successor's liveness record.
func RemoveWatchHeartbeat(root string, pid int) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	heartbeat, seen, err := ReadWatchHeartbeat(root)
	if err == nil && seen && heartbeat.PID != pid {
		return nil
	}
	path := filepath.Join(root, WatchHeartbeatFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vault: remove watch heartbeat: %w", err)
	}
	return nil
}

// WatchStalenessWindow is how old a heartbeat may be before status calls the
// watcher stopped: three tick intervals, floored at 30 seconds. A zero or
// negative interval selects the shipped five-minute default.
func WatchStalenessWindow(interval time.Duration) time.Duration {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	window := 3 * interval
	if window < 30*time.Second {
		return 30 * time.Second
	}
	return window
}

// ObservationCounter counts the observations that still need to be exported
// after a watermark. It is an interface so status can be tested without a
// database and so a store that cannot be read degrades to "pending: unknown"
// instead of blocking the rest of the report.
type ObservationCounter interface {
	CountObservationsSince(ctx context.Context, since time.Time) (int, error)
}

// StoreCounter is the read-only SQLite ObservationCounter used by
// `mnemos vault status`. It never migrates, creates, updates or vacuums the
// store; a missing or unreadable store is returned as an error, which status
// renders as "pending: unknown".
type StoreCounter struct {
	// Path is the database file to inspect.
	Path string
}

// CountObservationsSince counts observations whose created_at or
// invalidated_at is strictly after since. A zero since counts every row, the
// truthful answer for a vault that has never been exported.
//
// The comparison binds time.Time values, so the driver writes the same
// encoding the store's own writers use (time.Time.String() in UTC) and SQLite
// compares the two representations lexicographically. The test suite pins the
// boundary against rows written through memory.Service.
//
// The count is a watermark, not manifest.observations minus the current row
// count: export lists at most 10000 observations, rows can be deleted, and an
// invalidation is a pending export change that adds no row. The accepted cost
// is that a writer whose timestamp encoding is coarser (SQLite
// CURRENT_TIMESTAMP rows carry whole seconds) can sit on the same second as
// the export and escape a strict ">" comparison.
func (c StoreCounter) CountObservationsSince(ctx context.Context, since time.Time) (int, error) {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		return 0, fmt.Errorf("vault: observation counter: empty store path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("vault: observation counter: stat store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("vault: observation counter: store %s is not a regular file", path)
	}
	if populatedWAL(path) {
		// A read-only open of a WAL database needs the existing shared-memory
		// index. Refuse to run when it is absent: mode=ro would try to create
		// it, and status must never add files next to the store.
		if shm, err := os.Stat(path + "-shm"); err != nil || !shm.Mode().IsRegular() {
			return 0, fmt.Errorf("vault: observation counter: WAL sidecars incomplete next to %s; refusing a read-only open that could create them", path)
		}
	}
	db, err := sql.Open("sqlite", readOnlyStoreDSN(path))
	if err != nil {
		return 0, fmt.Errorf("vault: observation counter: open store: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var count int
	if err := db.QueryRowContext(queryCtx,
		`SELECT COUNT(*) FROM observations WHERE created_at > ? OR (invalidated_at IS NOT NULL AND invalidated_at > ?)`,
		since.UTC(), since.UTC()).Scan(&count); err != nil {
		return 0, fmt.Errorf("vault: observation counter: count pending observations: %w", err)
	}
	return count, nil
}

// StatusOptions selects the on-disk artifacts InspectStatus projects.
type StatusOptions struct {
	// Root is the configured vault directory.
	Root string
	// StorePath is the configured database path. It is only read.
	StorePath string
	// Interval is the configured watch tick; it sizes the heartbeat window.
	Interval time.Duration
	// Now overrides the comparison clock in tests. Zero means time.Now().
	Now time.Time
	// Counter overrides the store probe in tests. Nil uses
	// StoreCounter{Path: StorePath}.
	Counter ObservationCounter
}

// Status is the read-only projection rendered by `mnemos vault status`.
// Every field is derived from files on disk, never from configuration alone.
type Status struct {
	// Root is the vault directory that was inspected.
	Root string
	// DirExists reports whether the vault directory exists.
	DirExists bool

	// ManifestSeen reports that the export manifest file exists.
	ManifestSeen bool
	// ManifestError names why a present manifest could not be used.
	ManifestError string
	// LastExportedAt is the completion time of the last successful export.
	LastExportedAt time.Time
	// ExportedObservations is the observation count recorded by that export.
	ExportedObservations int
	// ExportedStore is the store path recorded by that export.
	ExportedStore string

	// Pending is the number of observations created or invalidated after the
	// export watermark.
	Pending int
	// PendingKnown is false when the store could not be read.
	PendingKnown bool
	// PendingError names why the pending count is unknown.
	PendingError string

	// HeartbeatSeen reports that the watch heartbeat file exists.
	HeartbeatSeen bool
	// HeartbeatError names why a present heartbeat could not be parsed.
	HeartbeatError string
	// HeartbeatAt is the most recent heartbeat time.
	HeartbeatAt time.Time
	// HeartbeatPID is the process that published the most recent heartbeat.
	HeartbeatPID int
	// WatcherRunning reports a heartbeat inside the staleness window.
	WatcherRunning bool
}

// InspectStatus derives export provenance, pending work and watcher liveness
// from the vault directory and the configured store. It creates and modifies
// nothing: missing artifacts read as "never", "all observations pending" and
// "stopped". A corrupt manifest turns the pending count into unknown rather
// than inventing a watermark.
func InspectStatus(ctx context.Context, opts StatusOptions) Status {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	status := Status{Root: opts.Root}
	if info, err := os.Stat(opts.Root); err == nil && info.IsDir() {
		status.DirExists = true
	}

	manifest, seen, err := ReadExportManifest(opts.Root)
	switch {
	case err != nil:
		status.ManifestSeen = seen
		status.ManifestError = err.Error()
	case seen && manifest.ExportedAt.IsZero():
		status.ManifestSeen = true
		status.ManifestError = ExportManifestFile + " has no exported_at"
	case seen:
		status.ManifestSeen = true
		status.LastExportedAt = manifest.ExportedAt.UTC()
		status.ExportedObservations = manifest.Observations
		status.ExportedStore = manifest.Store
	}

	if status.ManifestError == "" {
		counter := opts.Counter
		if counter == nil {
			counter = StoreCounter{Path: opts.StorePath}
		}
		pending, err := counter.CountObservationsSince(ctx, status.LastExportedAt)
		if err != nil {
			status.PendingError = err.Error()
		} else {
			status.PendingKnown = true
			status.Pending = pending
		}
	}

	heartbeat, beatSeen, beatErr := ReadWatchHeartbeat(opts.Root)
	switch {
	case beatErr != nil:
		status.HeartbeatSeen = beatSeen
		status.HeartbeatError = beatErr.Error()
	case beatSeen:
		status.HeartbeatSeen = true
		status.HeartbeatPID = heartbeat.PID
		status.HeartbeatAt = heartbeat.HeartbeatAt.UTC()
		if !heartbeat.HeartbeatAt.IsZero() {
			status.WatcherRunning = now.Sub(heartbeat.HeartbeatAt.UTC()) < WatchStalenessWindow(opts.Interval)
		}
	}
	return status
}

// populatedWAL reports whether a non-empty -wal file sits next to path. Its
// presence means committed data may live outside the main database file.
func populatedWAL(path string) bool {
	info, err := os.Stat(path + "-wal")
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// readOnlyStoreDSN builds a SQLite URI that cannot write the database. Without
// committed WAL content, immutable=1 further guarantees no locks and no
// -shm/-wal creation; with WAL content the caller has already required the
// existing shared-memory index, so mode=ro reads it without creating files.
func readOnlyStoreDSN(path string) string {
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
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(750)", "query_only(1)", "temp_store(MEMORY)"},
	}
	if !populatedWAL(path) {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// writeVaultJSONAtomic writes value as indented JSON to path through a temp
// file in the same directory and a rename, so readers see either the previous
// file or the complete new one.
func writeVaultJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mnemos-vault-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("set artifact permissions: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
