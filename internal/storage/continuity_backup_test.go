package storage_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/storage"
	"modernc.org/sqlite"
)

// The filesystem stays in place; this tests a same-machine SQLite snapshot,
// not a cross-machine import or a copy of an active main database file.
func TestContinuityOnlineBackupPreservesRetryAndRevisionIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, err := storage.Open(ctx, filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	clock := time.Date(2026, 10, 19, 12, 34, 56, 123456789, time.UTC)
	makeService := func(db *storage.DB) *continuity.Service {
		return continuity.New(continuity.Config{Store: db.Continuity(), Now: func() time.Time { return clock }})
	}
	svc := makeService(source)
	open := continuity.OpenInput{ProtocolVersion: 1, WorkspaceRoot: root, CWD: root, AgentID: "alice", HarnessKind: "pi", StartKey: "durable-start-key", Project: "backup-fixture"}
	started, err := svc.Open(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	save := continuity.CheckpointInput{ProtocolVersion: 1, WorkspaceRoot: root, CWD: root, AgentID: "alice", SessionID: started.SessionID, Action: "save", ExpectedRevision: &zero, RequestID: "durable-request", Payload: &continuity.Payload{Task: "backup", State: "active", Summary: "checkpoint before consistent backup"}}
	written, err := svc.Checkpoint(ctx, save)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "consistent-backup.db")
	conn, err := source.SQL().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(driverConn any) error {
		type backuper interface {
			NewBackup(string) (*sqlite.Backup, error)
		}
		provider, ok := driverConn.(backuper)
		if !ok {
			return fmt.Errorf("SQLite driver lacks online backup")
		}
		backup, err := provider.NewBackup(backupPath)
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = backup.Step(8)
			if err != nil {
				_ = backup.Finish()
				return err
			}
		}
		return backup.Finish()
	})
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the live source after backup. The recovered view must remain at
	// the snapshot generation, even though the source file remains open in WAL.
	one := int64(1)
	clear := continuity.CheckpointInput{ProtocolVersion: 1, WorkspaceRoot: root, CWD: root, AgentID: "alice", SessionID: started.SessionID, Action: "clear", ExpectedRevision: &one, RequestID: "source-clear"}
	clock = clock.Add(time.Hour)
	if _, err := svc.Checkpoint(ctx, clear); err != nil {
		t.Fatal(err)
	}
	restored, err := storage.Open(ctx, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	recovered := makeService(restored)
	confirmed, err := recovered.Open(ctx, open)
	if err != nil || confirmed.Status != "replayed" || confirmed.SessionID != started.SessionID || !confirmed.StartedAt.Equal(started.StartedAt) {
		t.Fatalf("startup retry lost %+v %v", confirmed, err)
	}
	retry, err := recovered.Checkpoint(ctx, save)
	if err != nil || retry.Status != "replayed" || retry.Revision != written.Revision || !retry.UpdatedAt.Equal(written.UpdatedAt) {
		t.Fatalf("checkpoint retry lost %+v %v", retry, err)
	}
	contextInput := continuity.ContextInput{ProtocolVersion: 1, WorkspaceRoot: root, CWD: root, AgentID: "alice", SessionID: started.SessionID, SourceSessionID: started.SessionID}
	before, err := recovered.Context(ctx, contextInput)
	if err != nil || before.Status != "selected" || before.Source.Revision != 1 || before.Source.Payload.Summary != save.Payload.Summary {
		t.Fatalf("source changes leaked to snapshot %+v %v", before, err)
	}
	clear.RequestID = "recovered-clear"
	cleared, err := recovered.Checkpoint(ctx, clear)
	if err != nil || cleared.Revision != 2 {
		t.Fatalf("restored CAS lost %+v %v", cleared, err)
	}
	if _, err := recovered.Checkpoint(ctx, save); !errors.Is(err, continuity.ErrConflict) {
		t.Fatalf("old request resurrected backup body %v", err)
	}
	two := int64(2)
	save.ExpectedRevision = &two
	save.RequestID = "recovered-new"
	save.Payload.Summary = "new checkpoint after restore"
	next, err := recovered.Checkpoint(ctx, save)
	if err != nil || next.Revision != 3 {
		t.Fatalf("revision reset after clear %+v %v", next, err)
	}
	live, err := svc.Context(ctx, contextInput)
	if err != nil || live.Status != "cleared" || live.Source.Revision != 2 {
		t.Fatalf("backup changes leaked into live store %+v %v", live, err)
	}
}
