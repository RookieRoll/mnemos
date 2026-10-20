package continuity_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type materializationAudit struct {
	continuity.Store
	bodies  int
	matched int64
}

func TestWindowPreservesNanosecondAndEquivalentTimestampOrdering(t *testing.T) {
	h := newHarness(t)
	a, b := h.open("", "a"), h.open("", "b")
	h.save(checkpoint(a.SessionID, "save-a", 0))
	h.save(checkpoint(b.SessionID, "save-b", 0))
	receiver := h.open("", "receiver")
	at := h.now
	if _, err := h.db.SQL().Exec(`UPDATE session_checkpoints SET updated_at=? WHERE session_id=?`, at.Format(time.RFC3339Nano), a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SQL().Exec(`UPDATE session_checkpoints SET updated_at=? WHERE session_id=?`, at.Add(time.Nanosecond).In(time.FixedZone("east", 8*3600)).Format(time.RFC3339Nano), b.SessionID); err != nil {
		t.Fatal(err)
	}
	in := continuity.ContextInput{ProtocolVersion: 1, SessionID: receiver.SessionID, Limit: 1}
	out, err := h.svc.Context(context.Background(), in)
	if err != nil || len(out.Candidates) != 1 || out.Candidates[0].SessionID != b.SessionID || !out.Ambiguous {
		t.Fatalf("nanosecond order changed: %+v %v", out, err)
	}
	if _, err := h.db.SQL().Exec(`UPDATE session_checkpoints SET updated_at=? WHERE session_id=?`, at.In(time.FixedZone("east", 8*3600)).Format(time.RFC3339Nano), b.SessionID); err != nil {
		t.Fatal(err)
	}
	out, err = h.svc.Context(context.Background(), in)
	want := a.SessionID
	if b.SessionID < want {
		want = b.SessionID
	}
	if err != nil || out.Candidates[0].SessionID != want {
		t.Fatalf("equivalent time ID tie changed: %+v %v", out, err)
	}
}

func TestWindowBoundsLegacySummaryAndRejectsSelectedCorruptBody(t *testing.T) {
	h := newHarness(t)
	summary := h.open("", "summary")
	if err := h.db.Sessions().Close(context.Background(), session.CloseInput{ID: summary.SessionID, Summary: strings.Repeat("中", 10000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SQL().Exec(`UPDATE sessions SET ended_at=? WHERE id=?`, h.now, summary.SessionID); err != nil {
		t.Fatal(err)
	}
	receiver := h.open("", "receiver")
	out := h.read(receiver.SessionID, summary.SessionID, "")
	if !utf8.ValidString(out.Source.Summary) || out.Source.Summary != strings.Repeat("中", 1365) {
		t.Fatalf("legacy summary prefix not bounded safely: %d bytes", len(out.Source.Summary))
	}
	bad := h.open("", "bad")
	h.save(checkpoint(bad.SessionID, "old", 0))
	h.now = h.now.Add(time.Second)
	good := h.open("", "good")
	h.save(checkpoint(good.SessionID, "new", 0))
	if _, err := h.db.SQL().Exec(`UPDATE session_checkpoints SET payload='not-json' WHERE session_id=?`, bad.SessionID); err != nil {
		t.Fatal(err)
	}
	// A manually corrupted, undisplayed body need not be deserialized. Reading
	// that exact body must still fail rather than pretend it is unavailable.
	in := continuity.ContextInput{ProtocolVersion: 1, SessionID: receiver.SessionID, Limit: 1}
	out, err := h.svc.Context(context.Background(), in)
	if err != nil || len(out.Candidates) != 1 || out.Candidates[0].SessionID != good.SessionID {
		t.Fatalf("out-of-window body was materialized: %+v %v", out, err)
	}
	in.SourceSessionID = bad.SessionID
	if _, err := h.svc.Context(context.Background(), in); err == nil || errors.Is(err, continuity.ErrUnavailable) {
		t.Fatalf("selected corruption must fail: %v", err)
	}
}

func TestWindowSnapshotSurvivesIndependentVisibilityWrites(t *testing.T) {
	h := newHarness(t)
	a, receiver := h.open("alice", "writer"), h.open("bob", "reader")
	in := checkpoint(a.SessionID, "v1", 0)
	in.AgentID = "alice"
	in.Visibility = "workspace"
	in.Payload.Summary = "v1"
	h.save(in)
	other, err := storage.Open(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer := continuity.New(continuity.Config{Store: other.Continuity(), Resolver: h.resolver, Now: func() time.Time { return h.now }})
	done := make(chan error, 1)
	go func() {
		for revision := int64(2); revision <= 30; revision++ {
			v := checkpoint(a.SessionID, fmt.Sprintf("v%d", revision), revision-1)
			v.AgentID = "alice"
			v.Payload.Summary = fmt.Sprintf("v%d", revision)
			if revision%2 == 0 {
				v.Visibility = "workspace"
			}
			if _, err := writer.Checkpoint(context.Background(), v); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 80 {
		out, err := h.svc.Context(context.Background(), continuity.ContextInput{ProtocolVersion: 1, AgentID: "bob", SessionID: receiver.SessionID, SourceSessionID: a.SessionID, Limit: 1})
		if errors.Is(err, continuity.ErrUnavailable) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if out.Source.Visibility != "workspace" || out.Source.Payload.Summary != fmt.Sprintf("v%d", out.Source.Revision) {
			t.Fatalf("torn visibility/body snapshot: %+v", out.Source)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (a *materializationAudit) Read(ctx context.Context, scope continuity.ReadScope) (*continuity.Snapshot, error) {
	snapshot, err := a.Store.Read(ctx, scope)
	if err == nil {
		a.bodies = len(snapshot.Sources)
		a.matched = snapshot.Matched
	}
	return snapshot, err
}

func TestContextMaterializesOnlyRequestedWindow(t *testing.T) {
	h := newHarness(t)
	for i := range 40 {
		opened := h.open("", fmt.Sprintf("history-%d", i))
		in := checkpoint(opened.SessionID, "save", 0)
		in.Payload.Task = fmt.Sprintf("task-%d", i)
		if i == 39 {
			in.Payload.State = "done"
		}
		h.save(in)
		h.now = h.now.Add(time.Second)
	}
	receiver := h.open("", "receiver")
	audit := &materializationAudit{Store: h.db.Continuity()}
	svc := continuity.New(continuity.Config{Store: audit, Resolver: h.resolver, Now: func() time.Time { return h.now }})
	in := continuity.ContextInput{ProtocolVersion: 1, SessionID: receiver.SessionID, Limit: 1, ResumeIntent: true}
	out, err := svc.Context(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if audit.bodies > 1 || audit.matched != 40 {
		t.Fatalf("materialized %d complete bodies for limit=1", audit.bodies)
	}
	if out.Status != "candidates" || !out.Ambiguous || !out.HasMore {
		t.Fatalf("window hid complete-set ambiguity: %+v", out)
	}
	in.Task = "task-0"
	out, err = svc.Context(context.Background(), in)
	if err != nil || out.Status != "selected" || out.Source.Payload.Task != "task-0" || audit.bodies != 1 {
		t.Fatalf("late exact task match lost: %+v bodies=%d err=%v", out, audit.bodies, err)
	}
}

func TestHiddenStaleDoneOrSummaryCannotManufactureUniqueFreshSource(t *testing.T) {
	for _, kind := range []string{"stale", "done", "summary"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			hidden := h.open("", "hidden")
			at := h.now.Add(-time.Hour)
			if kind == "summary" {
				if err := h.db.Sessions().Close(context.Background(), session.CloseInput{ID: hidden.SessionID, Summary: "finished previous work"}); err != nil {
					t.Fatal(err)
				}
				if _, err := h.db.SQL().Exec(`UPDATE sessions SET ended_at=? WHERE id=?`, at, hidden.SessionID); err != nil {
					t.Fatal(err)
				}
			} else {
				in := checkpoint(hidden.SessionID, "hidden", 0)
				if kind == "done" {
					in.Payload.State = "done"
				} else {
					at = h.now.Add(-25 * time.Hour)
				}
				h.save(in)
				if _, err := h.db.SQL().Exec(`UPDATE session_checkpoints SET updated_at=? WHERE session_id=?`, at, hidden.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			fresh := h.open("", "fresh")
			h.save(checkpoint(fresh.SessionID, "fresh", 0))
			receiver := h.open("", "receiver")
			out, err := h.svc.Context(context.Background(), continuity.ContextInput{ProtocolVersion: 1, SessionID: receiver.SessionID, Limit: 1, ResumeIntent: true})
			if err != nil || out.Status != "candidates" || !out.Ambiguous || !out.HasMore || out.Source != nil || len(out.Candidates) != 1 || out.Candidates[0].SessionID != fresh.SessionID {
				t.Fatalf("hidden %s erased ambiguity: %+v %v", kind, out, err)
			}
		})
	}
}
