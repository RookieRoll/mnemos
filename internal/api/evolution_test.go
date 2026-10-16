package api_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type conflictingEvidenceStore struct {
	memory.Store
	memory.EvolutionStore
}

func (s conflictingEvidenceStore) CheckEvidenceState(context.Context, memory.EvidenceCheckInput, time.Time) (*memory.EvidenceReference, error) {
	return nil, memory.ErrConflict
}

func TestEvidenceCheckHTTPConflictIsTyped(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "conflict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := conflictingEvidenceStore{Store: db.Observations()}
	svc := memory.NewService(memory.Config{Store: store})
	ts := httptest.NewServer(api.NewServer(api.Config{Memory: svc}).Handler())
	defer ts.Close()
	code, out := request(t, ts, "POST", "/v1/observations/id/evidence/check", "", map[string]any{"evidence_key": strings.Repeat("a", 64), "status": "changed"})
	if code != 409 || out["code"] != "conflict" {
		t.Fatalf("typed conflict lost: status=%d out=%v", code, out)
	}
}
