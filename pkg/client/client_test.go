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

func TestSaveRoundTrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/observations" || r.Method != "POST" {
			t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Observation": map[string]any{
				"ID":        "01HXYZ",
				"Title":     "t",
				"Type":      "pattern",
				"Pinned":    true,
				"CreatedAt": time.Now(),
			},
			"Deduped": false,
		})
	}))
	defer ts.Close()

	c := client.New(ts.URL)
	res, err := c.Save(context.Background(), client.SaveInput{
		Title: "t", Content: "c", Type: "pattern", Pinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Observation.ID != "01HXYZ" {
		t.Errorf("wrong ID: %s", res.Observation.ID)
	}
	if !res.Observation.Pinned {
		t.Errorf("pinned flag not carried: %v", res.Observation.Pinned)
	}
}

func TestPinRoundTrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/observations/01HXYZ/pin" || r.Method != "POST" {
			t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ID":        "01HXYZ",
			"Title":     "pinned obs",
			"Pinned":    payload["pinned"],
			"CreatedAt": time.Now(),
		})
	}))
	defer ts.Close()

	c := client.New(ts.URL)
	o, err := c.Pin(context.Background(), "01HXYZ", true)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Pinned {
		t.Errorf("expected Pinned=true, got %v", o.Pinned)
	}
}

func TestAuthHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc" {
			t.Errorf("missing auth header: %s", r.Header.Get("Authorization"))
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := client.New(ts.URL, client.WithAPIKey("abc"))
	if err := c.Healthz(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Get(context.Background(), "missing")
	if !client.IsNotFound(err) {
		t.Errorf("expected IsNotFound, got %v", err)
	}
}
