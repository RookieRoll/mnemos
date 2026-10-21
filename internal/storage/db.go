// Package storage provides the SQLite-backed persistence layer. It
// implements the Store interfaces declared by the memory, session, and
// skills packages, sharing a single database connection.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// DB wraps a *sql.DB with the concrete store implementations. One DB value
// backs all three domains (observations, sessions, skills).
type DB struct {
	sql         *sql.DB
	maintenance *sql.DB
}

// Open returns a ready-to-use DB at path. Parent directories are created if
// absent, WAL mode is enabled, and pending migrations are applied.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("storage: empty database path")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create dir %q: %w", dir, err)
		}
	}

	// Reserve the writer at BEGIN, before any schema/optimistic reads can
	// establish a snapshot that cannot later upgrade under WAL contention.
	// modernc applies this only to writable transactions; explicit ReadOnly
	// snapshots keep deferred BEGIN and can run alongside the reserved writer.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open sqlite: %w", err)
	}
	// SQLite is a single file; more than one writer is pointless and causes
	// busy retries. One writer + many readers is the supported shape, and
	// modernc.org/sqlite serialises writes internally.
	sqlDB.SetMaxOpenConns(1)

	if err := pingSQLite(ctx, sqlDB, 5*time.Second); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}

	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: migrate: %w", err)
	}

	maintenanceDB := sqlDB
	if !isMemoryDatabasePath(path) {
		// Claims and renewals use a separate, short-wait pool. The primary
		// pool deliberately has one connection; a long stage read must not
		// make its own lease heartbeat wait behind that connection. This pool
		// points at the same file and never runs migrations.
		maintenanceDSN := fmt.Sprintf("file:%s?_pragma=busy_timeout(75)&_txlock=immediate", path)
		maintenanceDB, err = sql.Open("sqlite", maintenanceDSN)
		if err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("storage: open maintenance connection: %w", err)
		}
		maintenanceDB.SetMaxOpenConns(1)
		if err := pingSQLite(ctx, maintenanceDB, time.Second); err != nil {
			_ = maintenanceDB.Close()
			_ = sqlDB.Close()
			return nil, fmt.Errorf("storage: ping maintenance connection: %w", err)
		}
	}

	return &DB{sql: sqlDB, maintenance: maintenanceDB}, nil
}

func isMemoryDatabasePath(path string) bool {
	lower := strings.ToLower(path)
	return lower == ":memory:" || strings.HasPrefix(lower, "file::memory:") ||
		(strings.HasPrefix(lower, "file:") && strings.Contains(lower, "mode=memory"))
}

type sqlitePinger interface {
	PingContext(context.Context) error
}

// SQLite's first WAL transition can return BUSY without exhausting its busy
// timeout. Retry only connection initialization, before migration/business SQL.
// The retry window bounds starting new attempts, not strict wall-clock time:
// modernc executes DSN pragmas using Background, so an in-flight open remains
// subject to the driver's existing busy_timeout rather than this context alone.
func pingSQLite(ctx context.Context, pinger sqlitePinger, window time.Duration) error {
	retryCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	var lastBusy error
	delay := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := retryCtx.Err(); err != nil {
			if lastBusy != nil {
				return lastBusy
			}
			return err
		}
		err := pinger.PingContext(retryCtx)
		if parentErr := ctx.Err(); parentErr != nil {
			return parentErr
		}
		if err == nil {
			return nil
		}
		if retryCtx.Err() != nil && lastBusy != nil && errors.Is(err, retryCtx.Err()) {
			return lastBusy
		}
		var coded interface{ Code() int }
		if !errors.As(err, &coded) || coded.Code()&0xff != 5 {
			return err
		}
		lastBusy = err
		if retryCtx.Err() != nil {
			return lastBusy
		}
		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			if parentErr := ctx.Err(); parentErr != nil {
				return parentErr
			}
			return lastBusy
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay *= 2
			if delay > 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
		}
	}
}

// Close releases the database handle.
func (d *DB) Close() error {
	if d.maintenance == nil || d.maintenance == d.sql {
		return d.sql.Close()
	}
	maintenanceErr := d.maintenance.Close()
	primaryErr := d.sql.Close()
	return errors.Join(primaryErr, maintenanceErr)
}

// SQL returns the underlying *sql.DB. Exposed for tests and advanced tooling
// (e.g., export/import). Normal code should use the Store interfaces.
func (d *DB) SQL() *sql.DB {
	return d.sql
}
