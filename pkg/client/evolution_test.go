package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestEvidenceCheckClientRoundTripAndConflict(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/observations/id/evidence/check" {
			t.Errorf("route=%s %s", r.Method, r.URL.Path)
		}
		var in client.EvidenceCheckInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		if in.EvidenceKey != "key" {
			t.Errorf("key=%s", in.EvidenceKey)
		}
		if in.Status == "matched" {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "conflict", "error": "no baseline"})
			return
		}
		_ = json.NewEncoder(w).Encode(client.EvidenceReference{Key: in.EvidenceKey, Locator: "docs/a.md", Relation: "supports", Status: in.Status, CheckedAt: &now})
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	out, err := c.CheckEvidence(context.Background(), client.EvidenceCheckInput{ObservationID: "id", EvidenceKey: "key", Status: "changed"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "changed" || out.CheckedAt == nil || !out.CheckedAt.Equal(now) {
		t.Fatalf("response=%+v", out)
	}
	_, err = c.CheckEvidence(context.Background(), client.EvidenceCheckInput{ObservationID: "id", EvidenceKey: "key", Status: "matched"})
	if !client.IsConflict(err) {
		t.Fatalf("typed conflict lost: %v", err)
	}
}
