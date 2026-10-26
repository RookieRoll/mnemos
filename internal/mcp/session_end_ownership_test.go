package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// newSessionEndHarness builds the in-memory MCP harness while keeping the
// database and the exact session service so a case can seed foreign or
// continuity-bound sessions and inspect what stayed open.
func newSessionEndHarness(t *testing.T, adjust func(*testing.T, *mcp.Config, *storage.DB, *session.Service)) (*harness, *storage.DB, *session.Service) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sessions := session.NewService(session.Config{Store: db.Sessions()})
	cfg := mcp.Config{
		Name:     "mnemos",
		Profile:  "maintenance",
		Version:  "test",
		Memory:   memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: sessions,
		Skills:   skills.NewService(skills.Config{Store: db.Skills()}),
		Touches:  db.Touches(),
		Prewarm: prewarm.NewService(prewarm.Config{
			Observations: db.Observations(),
			Sessions:     db.Sessions(),
			Skills:       db.Skills(),
			Touches:      db.Touches(),
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if adjust != nil {
		adjust(t, &cfg, db, sessions)
	}
	srv := mcp.NewServer(cfg)

	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.SDK().Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client connect: %v", err)
	}
	h := &harness{t: t, client: clientSession, server: serverSession}
	t.Cleanup(h.close)
	return h, db, sessions
}

// withContinuityFlow wires the unified task-session flow with a real
// continuity core so receipt-backed closes reach the MCP surface.
func withContinuityFlow(_ *testing.T, cfg *mcp.Config, db *storage.DB, sessions *session.Service) {
	cfg.Flow = continuityflow.New(continuityflow.Config{
		Core:           continuity.New(continuity.Config{Store: db.Continuity()}),
		LegacySessions: sessions,
	})
}

// openReceiptSession commits one continuity-bound session in root through the
// real continuity service, returning the session row that carries the receipt.
func openReceiptSession(t *testing.T, db *storage.DB, agent, root string) *session.Session {
	t.Helper()
	core := continuity.New(continuity.Config{Store: db.Continuity()})
	out, err := core.Open(context.Background(), continuity.OpenInput{
		ProtocolVersion: 1, AgentID: agent, CWD: root, WorkspaceRoot: root,
		HarnessKind: "test", StartKey: "start-" + agent,
	})
	if err != nil {
		t.Fatalf("open continuity session: %v", err)
	}
	sess, err := db.Sessions().Get(context.Background(), out.SessionID)
	if err != nil {
		t.Fatalf("read continuity session: %v", err)
	}
	if !sess.ContinuityBound {
		t.Fatal("continuity session is not marked bound")
	}
	return sess
}

// maybeSession reads a session row, translating the expected miss to nil so a
// rejection case can still assert that nothing was written for an unknown id.
func maybeSession(t *testing.T, sessions *session.Service, id string) *session.Session {
	t.Helper()
	sess, err := sessions.Get(context.Background(), id)
	if errors.Is(err, session.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	return sess
}

// startSessionViaTool opens one legacy session through the MCP surface and
// returns its id, so a case exercises the exact receipt an agent receives.
func startSessionViaTool(t *testing.T, h *harness, project string) string {
	t.Helper()
	_, text := h.call("mnemos_session_start", map[string]any{"project": project, "goal": "g"})
	var started map[string]any
	if err := json.Unmarshal([]byte(text), &started); err != nil {
		t.Fatalf("session_start result: %v (%s)", err, text)
	}
	id, _ := started["session_id"].(string)
	if id == "" {
		t.Fatalf("session_start returned no session id: %s", text)
	}
	return id
}

func TestSessionEndOwnershipOverMCP(t *testing.T) {
	type testCase struct {
		name       string
		adjust     func(*testing.T, *mcp.Config, *storage.DB, *session.Service)
		target     func(*testing.T, *harness, *storage.DB, *session.Service) string
		verify     func(*testing.T, *session.Service, string)
		wantCode   string
		wantClosed bool
	}
	cases := []testCase{
		{
			name: "bound session closes",
			target: func(t *testing.T, h *harness, _ *storage.DB, _ *session.Service) string {
				return startSessionViaTool(t, h, "p")
			},
			wantClosed: true,
		},
		{
			name: "bound session in the server project closes",
			adjust: func(_ *testing.T, cfg *mcp.Config, _ *storage.DB, _ *session.Service) {
				cfg.DefaultProject = "p"
			},
			target: func(t *testing.T, h *harness, _ *storage.DB, _ *session.Service) string {
				return startSessionViaTool(t, h, "p")
			},
			wantClosed: true,
		},
		{
			name: "foreign uuidv7 rejected and left open",
			target: func(t *testing.T, _ *harness, _ *storage.DB, sessions *session.Service) string {
				foreign, err := sessions.Open(context.Background(), session.OpenInput{Project: "p", Goal: "foreign"})
				if err != nil {
					t.Fatal(err)
				}
				// Windows clocks can tick in ~15 ms steps; the bound-session
				// seam orders by started_at, so the two rows must not tie.
				time.Sleep(20 * time.Millisecond)
				if _, err := sessions.Open(context.Background(), session.OpenInput{Project: "p", Goal: "bound"}); err != nil {
					t.Fatal(err)
				}
				return foreign.ID
			},
			wantCode: "session_not_owned",
		},
		{
			name:   "receipt authorizes another agent's session in one workspace",
			adjust: withContinuityFlow,
			target: func(t *testing.T, _ *harness, db *storage.DB, sessions *session.Service) string {
				// The invocation's bound session belongs to a different agent
				// than the receipt owner; only the workspace may decide.
				if _, err := sessions.Open(context.Background(), session.OpenInput{Project: "p", AgentID: "agent-b", Goal: "bound"}); err != nil {
					t.Fatal(err)
				}
				root := t.TempDir()
				target := openReceiptSession(t, db, "agent-a", root)
				t.Chdir(root)
				return target.ID
			},
			verify: func(t *testing.T, sessions *session.Service, target string) {
				sess := maybeSession(t, sessions, target)
				if sess == nil || sess.AgentID != "agent-a" {
					t.Fatalf("receipt owner lost: %+v", sess)
				}
			},
			wantClosed: true,
		},
		{
			name:   "receipt from another workspace rejected and left open",
			adjust: withContinuityFlow,
			target: func(t *testing.T, _ *harness, db *storage.DB, _ *session.Service) string {
				receiptRoot := t.TempDir()
				invocationRoot := t.TempDir()
				target := openReceiptSession(t, db, "agent-a", receiptRoot)
				t.Chdir(invocationRoot)
				return target.ID
			},
			wantCode: "session_not_owned",
		},
		{
			name: "no resolvable binding and no receipt rejected",
			adjust: func(_ *testing.T, cfg *mcp.Config, _ *storage.DB, _ *session.Service) {
				cfg.DefaultProject = "api"
			},
			target: func(t *testing.T, _ *harness, _ *storage.DB, sessions *session.Service) string {
				foreign, err := sessions.Open(context.Background(), session.OpenInput{Project: "web", Goal: "foreign"})
				if err != nil {
					t.Fatal(err)
				}
				return foreign.ID
			},
			wantCode: "session_not_owned",
		},
		{
			name: "unknown id uses the same code",
			target: func(t *testing.T, _ *harness, _ *storage.DB, sessions *session.Service) string {
				if _, err := sessions.Open(context.Background(), session.OpenInput{Project: "api", Goal: "bound"}); err != nil {
					t.Fatal(err)
				}
				return ulid.Make().String()
			},
			wantCode: "session_not_owned",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, db, sessions := newSessionEndHarness(t, tc.adjust)
			target := tc.target(t, h, db, sessions)
			res, text := h.call("mnemos_session_end", map[string]any{
				"session_id": target, "summary": "done", "status": "ok",
			})
			if tc.wantCode == "" {
				if res.IsError {
					t.Fatalf("session_end failed: %s", text)
				}
				if !strings.Contains(text, "closed (ok)") {
					t.Fatalf("session_end did not confirm closure: %s", text)
				}
				if tc.wantClosed {
					sess := maybeSession(t, sessions, target)
					if sess == nil || sess.EndedAt == nil {
						t.Fatal("accepted session stayed open")
					}
				}
				if tc.verify != nil {
					tc.verify(t, sessions, target)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("want %s, got success: %s", tc.wantCode, text)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(text), &body); err != nil {
				t.Fatalf("rejection is not JSON: %v (%s)", err, text)
			}
			if body["code"] != tc.wantCode {
				t.Errorf("stable code = %v, want %s (%s)", body["code"], tc.wantCode, text)
			}
			if body["session_id"] != target {
				t.Errorf("rejection names %v, want %s", body["session_id"], target)
			}
			guidance, _ := body["error"].(string)
			if !strings.Contains(guidance, "mnemos sessions close "+target) {
				t.Errorf("rejection text must name the supported route: %q", guidance)
			}
			if sess := maybeSession(t, sessions, target); sess != nil && sess.EndedAt != nil {
				t.Fatal("rejected session was closed")
			}
		})
	}
}

func TestSessionEndUnknownIDGuidanceIsStable(t *testing.T) {
	h, _, _ := newSessionEndHarness(t, nil)
	res, text := h.call("mnemos_session_end", map[string]any{
		"session_id": ulid.Make().String(), "summary": "done",
	})
	if !res.IsError {
		t.Fatalf("unknown id must fail with the ownership code: %s", text)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "session_not_owned" {
		t.Fatalf("code = %v, want session_not_owned (%s)", body["code"], text)
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "close the current session") {
		t.Errorf("guidance must name closing the current session: %v", body["error"])
	}
}
