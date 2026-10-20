package continuity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func capacityFixture(tb testing.TB, count int, large bool) (*continuity.Service, *materializationAudit) {
	tb.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(tb.TempDir(), "capacity.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	payload := continuity.Payload{Task: "capacity", State: "blocked", Summary: "small checkpoint", Workspace: continuity.WorkspaceState{Known: true, Branch: "main", HEAD: "commit-one"}}
	if large {
		payload.Summary = strings.Repeat("s", 4096)
		for range 8 {
			payload.NextSteps = append(payload.NextSteps, strings.Repeat("n", 500))
			payload.Blockers = append(payload.Blockers, strings.Repeat("b", 500))
		}
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > continuity.MaxPayloadBytes {
		tb.Fatalf("invalid capacity body %d: %v", len(body), err)
	}
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	for i := range count + 1 {
		id := fmt.Sprintf("source-%08d", i)
		if i == count {
			id = "receiver"
		}
		at := now.Add(time.Duration(i))
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,agent_id,project,started_at) VALUES(?,'default','p',?)`, id, at); err != nil {
			tb.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_continuity_bindings(session_id,agent_id,workspace_key,workspace_root,harness_kind,start_key,start_request_hash) VALUES(?,'default','test-workspace','test-root','test',?,'hash')`, id, id); err != nil {
			tb.Fatal(err)
		}
		if i < count {
			if _, err := tx.ExecContext(ctx, `INSERT INTO session_checkpoints(session_id,revision,payload,visibility,updated_at,last_request_id,last_request_hash) VALUES(?,1,?,'owner',?,'save','hash')`, id, string(body), at); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	audit := &materializationAudit{Store: db.Continuity()}
	resolver := &fixtureResolver{workspace: continuity.Workspace{Key: "test-workspace", Root: "test-root", State: payload.Workspace}}
	return continuity.New(continuity.Config{Store: audit, Resolver: resolver, Now: func() time.Time { return now.Add(time.Hour) }}), audit
}

// BenchmarkContinuityCandidateWindow varies retained history and body size at
// a fixed display limit. bodies/op audits returned snapshot bodies; together
// with the window-only loader and allocation results it demonstrates the body
// materialization bound. Total allocations and metadata scan CPU remain linear
// in history size. This is not a retention policy or a latency percentage claim.
func BenchmarkContinuityCandidateWindow(b *testing.B) {
	for _, count := range []int{128, 2048} {
		for _, large := range []bool{false, true} {
			b.Run(fmt.Sprintf("history%d/large%t", count, large), func(b *testing.B) {
				svc, audit := capacityFixture(b, count, large)
				input := continuity.ContextInput{ProtocolVersion: 1, SessionID: "receiver", Limit: 5}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := svc.Context(context.Background(), input); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(audit.bodies), "bodies/op")
				b.ReportMetric(float64(audit.matched), "matches/op")
			})
		}
	}
}
