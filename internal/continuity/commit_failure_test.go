package continuity_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

func TestOpenBindingFailureRollsBackInsertedSession(t *testing.T) {
	h := newHarness(t)
	existing := h.open("", "existing")
	// The trigger only fails once the new session really exists in this
	// transaction, so this exercises rollback after the first durable write.
	_, err := h.db.SQL().Exec(`CREATE TRIGGER fail_continuity_binding
		BEFORE INSERT ON session_continuity_bindings
		WHEN EXISTS (SELECT 1 FROM sessions WHERE id=NEW.session_id)
		BEGIN SELECT RAISE(ABORT, 'injected binding failure after session insert'); END`)
	if err != nil {
		t.Fatal(err)
	}
	in := continuity.OpenInput{ProtocolVersion: 1, Project: "api", HarnessKind: "test", StartKey: "binding-failure"}
	out, err := h.svc.Open(context.Background(), in)
	if err == nil || out != nil || !strings.Contains(err.Error(), "injected binding failure after session insert") {
		t.Fatalf("failed binding returned a success receipt or missed injection: %+v %v", out, err)
	}
	for _, table := range []string{"sessions", "session_continuity_bindings"} {
		var count int
		if err := h.db.SQL().QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("partial open remained in %s: count=%d err=%v", table, count, err)
		}
	}
	if _, err := h.db.Sessions().Get(context.Background(), existing.SessionID); err != nil {
		t.Fatalf("rollback damaged prior session: %v", err)
	}
	if _, err := h.db.SQL().Exec(`DROP TRIGGER fail_continuity_binding`); err != nil {
		t.Fatal(err)
	}
	opened, err := h.svc.Open(context.Background(), in)
	if err != nil || opened == nil || opened.Status != "applied" || opened.SessionID == existing.SessionID {
		t.Fatalf("failed open left a retry identity or poisoned connection: %+v %v", opened, err)
	}
	for _, table := range []string{"sessions", "session_continuity_bindings"} {
		var count int
		if err := h.db.SQL().QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("retry did not create exactly one session/binding in %s: %d %v", table, count, err)
		}
	}
}

func TestCheckpointCommitFailurePreservesVersionAndRetryIdentity(t *testing.T) {
	h := newHarness(t)
	a := h.open("", "checkpoint-commit")
	firstInput := checkpoint(a.SessionID, "committed-request", 0)
	first := h.save(firstInput)
	// An immediate trigger failure would only exercise Exec. A deferred FK
	// permits the checkpoint UPDATE and trigger INSERT, then rejects COMMIT.
	for _, statement := range []string{
		`CREATE TABLE continuity_failure_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE continuity_failure_probe (parent_id INTEGER REFERENCES continuity_failure_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_checkpoint_commit AFTER UPDATE ON session_checkpoints
		 BEGIN INSERT INTO continuity_failure_probe(parent_id) VALUES(42); END`,
	} {
		if _, err := h.db.SQL().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	type storedVersion struct {
		revision             int64
		payload, requestID   string
		requestHash, updated string
	}
	readVersion := func() storedVersion {
		t.Helper()
		var version storedVersion
		if err := h.db.SQL().QueryRow(`SELECT revision,payload,last_request_id,last_request_hash,CAST(updated_at AS TEXT) FROM session_checkpoints WHERE session_id=?`, a.SessionID).Scan(&version.revision, &version.payload, &version.requestID, &version.requestHash, &version.updated); err != nil {
			t.Fatal(err)
		}
		return version
	}
	before := readVersion()
	failedInput := checkpoint(a.SessionID, "uncommitted-request", 1)
	failedInput.Payload.Summary = "new body must never become visible after failed commit"
	out, err := h.svc.Checkpoint(context.Background(), failedInput)
	if err == nil || out != nil || !strings.Contains(err.Error(), "commit checkpoint") || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("expected real COMMIT failure without success receipt, got %+v %v", out, err)
	}
	if after := readVersion(); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed commit changed checkpoint/body/request identity: before=%+v after=%+v", before, after)
	}
	var probes int
	if err := h.db.SQL().QueryRow(`SELECT count(*) FROM continuity_failure_probe`).Scan(&probes); err != nil || probes != 0 {
		t.Fatalf("failed transaction leaked trigger writes: %d %v", probes, err)
	}
	if source := h.read(a.SessionID, a.SessionID, ""); source.Source == nil || source.Source.Payload.Summary != firstInput.Payload.Summary || source.Source.Revision != first.Revision {
		t.Fatalf("failed body surfaced to reader: %+v", source)
	}
	if replay := h.save(firstInput); replay.Status != "replayed" || replay.Revision != first.Revision || !replay.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("last committed request lost exact retry: %+v", replay)
	}
	if _, err := h.db.SQL().Exec(`DROP TRIGGER fail_checkpoint_commit`); err != nil {
		t.Fatal(err)
	}
	if retry := h.save(failedInput); retry.Status != "applied" || retry.Revision != 2 {
		t.Fatalf("failed request was falsely recorded as committed: %+v", retry)
	}
}
