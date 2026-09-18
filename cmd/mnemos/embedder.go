package main

import (
	"context"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/embedding"
	"github.com/polyxmedia/mnemos/internal/memory"
)

// selectEmbedder resolves the configured embedder, auto-probing Ollama
// when provider="auto". Returns a memory.Embedder (the narrow interface
// the memory package consumes) to keep the import graph clean.
func selectEmbedder(ctx context.Context, cfg config.EmbeddingConfig) memory.Embedder {
	switch cfg.Provider {
	case "none":
		return embedding.NewNoop()
	case "ollama":
		return embedding.NewOllama(embedding.OllamaConfig{
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			Dimension: cfg.Dimension,
		})
	case "openai":
		return embedding.NewOpenAI(embedding.OpenAIConfig{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			Model:     cfg.Model,
			Dimension: cfg.Dimension,
		})
	default: // "auto"
		// An explicit base_url is read as the operator saying where the server
		// is, so it is probed first. Probing only Ollama here meant that an
		// OpenAI-compatible deployment with provider left at its default
		// silently degraded to Noop — no vector search, and nothing in any
		// command's output to explain why.
		if cfg.BaseURL != "" && embedding.ProbeOpenAI(ctx, cfg.BaseURL, cfg.APIKey) {
			return embedding.NewOpenAI(embedding.OpenAIConfig{
				BaseURL:   cfg.BaseURL,
				APIKey:    cfg.APIKey,
				Model:     cfg.Model,
				Dimension: cfg.Dimension,
			})
		}
		if embedding.ProbeOllama(ctx, cfg.BaseURL) {
			return embedding.NewOllama(embedding.OllamaConfig{
				BaseURL:   cfg.BaseURL,
				Model:     cfg.Model,
				Dimension: cfg.Dimension,
			})
		}
		return embedding.NewNoop()
	}
}
