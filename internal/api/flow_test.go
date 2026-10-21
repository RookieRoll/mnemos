package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type maintenanceProbe struct{ calls int }

func (p *maintenanceProbe) MaybeRun(context.Context) error {
	p.calls++
	return nil
}

func TestHTTPSessionStartUsesFlowButReadOnlyContextDoesNotRunMaintenance(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions := session.NewService(session.Config{Store: db.Sessions()})
	composer := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills(), Touches: db.Touches(),
	})
	probe := &maintenanceProbe{}
	flow := continuityflow.New(continuityflow.Config{
		Core: continuity.New(continuity.Config{Store: db.Continuity()}), LegacySessions: sessions,
		Composer: composer, Maintenance: probe,
	})
	server := api.NewServer(api.Config{
		Flow: flow, Memory: memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: sessions, Skills: skills.NewService(skills.Config{Store: db.Skills()}),
		Touches: db.Touches(), Prewarm: composer,
	})
	handler := server.Handler()

	legacyBody := `{"project":"shared-label","goal":"task"}`
	legacyReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(legacyBody))
	legacyReq.Header.Set("Content-Type", "application/json")
	legacyResp := httptest.NewRecorder()
	handler.ServeHTTP(legacyResp, legacyReq)
	if legacyResp.Code != http.StatusCreated || probe.calls != 1 {
		t.Fatalf("legacy session start status=%d maintenance=%d body=%s", legacyResp.Code, probe.calls, legacyResp.Body.String())
	}
	var legacyOut map[string]any
	if err := json.Unmarshal(legacyResp.Body.Bytes(), &legacyOut); err != nil {
		t.Fatal(err)
	}
	if legacyOut["session_id"] == "" || legacyOut["prewarm"] == nil {
		t.Fatalf("legacy HTTP envelope changed: %#v", legacyOut)
	}

	root := t.TempDir()
	opened, err := flow.Open(ctx, continuity.OpenInput{
		ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, HarnessKind: "test", StartKey: "read-only", Project: "shared-label",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe.calls = 0
	contextBody, err := json.Marshal(continuity.ContextInput{
		ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, SessionID: opened.SessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/continuity/context", strings.NewReader(string(contextBody)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if probe.calls != 0 {
		t.Fatalf("read-only context invoked maintenance %d times", probe.calls)
	}
}
