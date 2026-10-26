package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func runtimeSQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate", path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationRechecksStalePlanUnderWriterTransaction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db := runtimeSQL(t, path)
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var pending migration
	for _, m := range all {
		if m.version < 9 {
			if err := applyMigration(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
		if m.version == 9 {
			pending = m
		}
	}
	const writers = 8
	handles := make([]*sql.DB, writers)
	for i := range handles {
		handles[i] = runtimeSQL(t, path)
		applied, err := appliedVersions(ctx, handles[i])
		if err != nil || applied[9] {
			t.Fatalf("stale plan fixture: %v %v", applied, err)
		}
	}
	// Every independent handle already planned version nine before any writer
	// starts. This deterministically reproduces the real upgrade interleaving.
	gate := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for _, handle := range handles {
		wg.Add(1)
		go func(db *sql.DB) { defer wg.Done(); <-gate; errs <- applyMigration(ctx, db, pending) }(handle)
	}
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent stale upgrade: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=9`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("version committed %d times: %v", count, err)
	}
}

func TestConcurrentFreshMigrationsAndFailedDDLAtomicity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.db")
	a, b := runtimeSQL(t, path), runtimeSQL(t, path)
	results := make(chan error, 2)
	for _, handle := range []*sql.DB{a, b} {
		go func(db *sql.DB) {
			if err := pingSQLite(ctx, db, 5*time.Second); err != nil {
				results <- err
				return
			}
			results <- migrate(ctx, db)
		}(handle)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	err := applyMigration(ctx, a, migration{version: 999, name: "failure", sql: `CREATE TABLE runtime_partial(id INTEGER); INSERT INTO missing_runtime_table VALUES(1);`})
	if err == nil {
		t.Fatal("invalid DDL unexpectedly committed")
	}
	for _, query := range []string{`SELECT count(*) FROM sqlite_master WHERE name='runtime_partial'`, `SELECT count(*) FROM schema_migrations WHERE version=999`} {
		var n int
		if err := a.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatalf("failed migration leaked state: %d %v", n, err)
		}
	}
}

func TestConcurrentStorageOpenFresh(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "open-fresh.db")
	const clients = 8
	type result struct {
		db  *DB
		err error
	}
	results := make(chan result, clients)
	gate := make(chan struct{})
	for range clients {
		go func() { <-gate; db, err := Open(ctx, path); results <- result{db, err} }()
	}
	close(gate)
	var handles []*DB
	defer func() {
		for _, db := range handles {
			_ = db.Close()
		}
	}()
	for range clients {
		opened := <-results
		if opened.err != nil {
			t.Errorf("concurrent production Open: %v", opened.err)
			continue
		}
		handles = append(handles, opened.db)
	}
	if len(handles) != clients {
		return
	}
	var count, latest int
	if err := handles[0].SQL().QueryRow(`SELECT count(*),max(version) FROM schema_migrations`).Scan(&count, &latest); err != nil || count != 12 || latest != 12 {
		t.Fatalf("fresh opens diverged: count=%d latest=%d %v", count, latest, err)
	}
}
