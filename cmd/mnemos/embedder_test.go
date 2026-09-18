package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
)

// TestSelectEmbedderAutoPrefersAnExplicitBaseURL is the regression this
// change exists for: provider left at its default "auto" with an
// OpenAI-compatible base_url configured used to degrade to Noop silently,
// because only Ollama was probed. The symptom was vector search never
// turning on, with nothing in any command's output saying why.
func TestSelectEmbedderAutoPrefersAnExplicitBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 405 is what OpenAI itself answers a GET on /v1/embeddings with, and
		// it must still count as "the route is there".
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	got := selectEmbedder(context.Background(), config.EmbeddingConfig{
		Provider:  "auto",
		BaseURL:   srv.URL + "/v1",
		Model:     "bge-small-zh-v1.5",
		Dimension: 512,
	})
	if got.Dimension() == 0 {
		t.Fatal("auto with a reachable base_url must not degrade to Noop")
	}
	if want := "openai/bge-small-zh-v1.5"; got.Model() != want {
		t.Errorf("Model() = %q, want %q", got.Model(), want)
	}
}

// TestSelectEmbedderAutoFallsBackToNoop keeps the degraded-silent contract:
// with nothing reachable, hybrid search stays off rather than erroring.
func TestSelectEmbedderAutoFallsBackToNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	got := selectEmbedder(context.Background(), config.EmbeddingConfig{
		Provider:  "auto",
		BaseURL:   srv.URL + "/v1",
		Dimension: 512,
	})
	if got.Dimension() != 0 {
		t.Errorf("an unreachable base_url must degrade to Noop, got dimension %d", got.Dimension())
	}
}

// TestSelectEmbedderExplicitProviders covers the two branches that the auto
// path must not have changed.
func TestSelectEmbedderExplicitProviders(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		got := selectEmbedder(context.Background(), config.EmbeddingConfig{Provider: "none"})
		if got.Dimension() != 0 {
			t.Errorf("none must be Noop, got dimension %d", got.Dimension())
		}
	})

	t.Run("openai without probing", func(t *testing.T) {
		// An explicit provider must be honoured even when the probe would fail;
		// the operator has already answered the question the probe asks.
		got := selectEmbedder(context.Background(), config.EmbeddingConfig{
			Provider:  "openai",
			BaseURL:   "http://127.0.0.1:1/v1",
			Model:     "m",
			Dimension: 512,
		})
		if got.Dimension() != 512 {
			t.Errorf("explicit openai must be constructed as-is, got dimension %d", got.Dimension())
		}
	})

	t.Run("ollama without probing", func(t *testing.T) {
		got := selectEmbedder(context.Background(), config.EmbeddingConfig{
			Provider:  "ollama",
			BaseURL:   "http://127.0.0.1:1",
			Model:     "m",
			Dimension: 768,
		})
		if got.Dimension() != 768 {
			t.Errorf("explicit ollama must be constructed as-is, got dimension %d", got.Dimension())
		}
	})
}
