package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestEvolutionWriterWaitsForIndependentReservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "writer.db")
	one, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	for _, db := range []*DB{one, two} {
		var timeout int
		if err := db.SQL().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Fatalf("connection busy_timeout=%d err=%v", timeout, err)
		}
	}
	held, err := startEvolutionWrite(ctx, one.SQL())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	// A WAL reader is not a competing writer: explicitly read-only snapshots
	// must still begin and read while the first connection reserves writes.
	reader, err := two.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&count); err != nil {
		_ = reader.Rollback()
		t.Fatal(err)
	}
	if err := reader.Commit(); err != nil {
		t.Fatal(err)
	}
	type result struct {
		tx  *sql.Tx
		err error
	}
	ready := make(chan struct{})
	done := make(chan result, 1)
	go func() { close(ready); tx, err := startEvolutionWrite(ctx, two.SQL()); done <- result{tx, err} }()
	<-ready
	select {
	case r := <-done:
		if r.tx != nil {
			_ = r.tx.Rollback()
		}
		t.Fatalf("second writer did not wait for held reservation: %v", r.err)
	case <-time.After(40 * time.Millisecond):
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if err := r.tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("waiting writer did not resume after reservation released")
	}
}

func TestEvolutionWriterBusyTimeoutReturnsConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "timeout.db")
	one, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	// Keep this test bounded without changing the production 5-second policy.
	if _, err := two.SQL().ExecContext(ctx, "PRAGMA busy_timeout=60"); err != nil {
		t.Fatal(err)
	}
	held, err := startEvolutionWrite(ctx, one.SQL())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	failed, err := startEvolutionWrite(ctx, two.SQL())
	if failed != nil {
		_ = failed.Rollback()
		t.Fatal("timed-out acquisition returned a transaction")
	}
	var code interface{ Code() int }
	if err == nil || !errors.As(err, &code) || code.Code()&255 != 5 {
		t.Fatalf("want SQLITE_BUSY after busy timeout, got %v", err)
	}
	if inUse := two.SQL().Stats().InUse; inUse != 0 {
		t.Fatalf("failed acquisition leaked %d connection(s)", inUse)
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	resumed, err := startEvolutionWrite(ctx, two.SQL())
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Rollback(); err != nil {
		t.Fatal(err)
	}
}
