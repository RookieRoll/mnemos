package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestScopedReadClientRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	zero := 0
	ten := 10
	batch := client.GetManyInput{IDs: []string{"a", "a", "b"}, Project: "p", AgentID: "agent", IncludeRaw: true, IncludeStale: true, AsOf: now, MaxBytes: 65536}
	neighborhood := client.NeighborhoodInput{ID: "a", Project: "p", AgentID: "agent", Before: &zero, After: &ten, IncludeRaw: true, IncludeStale: true, AsOf: now, MaxBytes: 65536}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method=%s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/observations/batch":
			var got client.GetManyInput
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, batch) {
				t.Errorf("batch options lost: %+v", got)
			}
			_ = json.NewEncoder(w).Encode(client.GetManyResult{Items: []client.ReadResult{{ID: "a", Status: client.ReadOK, Observation: &client.Observation{ID: "a", AgentID: "agent", TrustTier: "raw", SourceKind: "tool", DerivedFrom: []string{"source"}, ValidFrom: now, ValidUntil: &now, InvalidatedAt: &now, ExpiresAt: &now}}, {ID: "b", Status: client.ReadOmittedForSize}}, MaxBytes: 65536, BytesReturned: 512, ContentLimited: true})
		case "/v1/observations/neighborhood":
			var got client.NeighborhoodInput
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, neighborhood) {
				t.Errorf("neighborhood options lost: %+v", got)
			}
			_ = json.NewEncoder(w).Encode(client.NeighborhoodResult{AnchorID: "a", Items: []client.SummaryResult{{ID: "a", Status: client.ReadOK, Summary: &client.ObservationSummary{ID: "a", TrustTier: "raw", ValidUntil: &now, TitleTruncated: true, SnippetTruncated: true}}}, Window: &client.NeighborhoodWindow{AfterLimit: 10, HasMoreAfter: true}, MaxBytes: 65536, BytesReturned: 128, ContentLimited: true})
		default:
			t.Errorf("path=%s", r.URL.Path)
		}
	}))
	defer ts.Close()
	c := client.New(ts.URL)
	b, err := c.GetMany(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if b.Items[0].Observation.TrustTier != "raw" || b.Items[0].Observation.ValidUntil == nil || !b.ContentLimited || b.BytesReturned != 512 || b.Items[1].Status != client.ReadOmittedForSize {
		t.Fatalf("batch response lost: %+v", b)
	}
	n, err := c.Neighborhood(context.Background(), neighborhood)
	if err != nil {
		t.Fatal(err)
	}
	if n.Items[0].Summary.TrustTier != "raw" || !n.Items[0].Summary.TitleTruncated || !n.Items[0].Summary.SnippetTruncated || !n.Window.HasMoreAfter || !n.ContentLimited {
		t.Fatalf("summary metadata lost: %+v", n)
	}
}
