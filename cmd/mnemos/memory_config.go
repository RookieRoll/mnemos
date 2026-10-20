package main

import (
	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/memory"
)

// memoryServiceConfig keeps all retrieval entry points on the same ranking
// contract. It only combines already-resolved dependencies; capability and
// continuity-only callers need not probe an embedding provider to use it.
func memoryServiceConfig(search config.SearchConfig, store memory.Store, embedder memory.Embedder, recorder memory.InjectionRecorder) memory.Config {
	rank := memory.DefaultRankParams()
	rank.DecayRate = search.DecayRate
	hybrid := memory.DefaultHybridParams()
	hybrid.Alpha = search.HybridAlpha
	return memory.Config{Store: store, Embedder: embedder, Injections: recorder, RankParams: rank, Hybrid: hybrid,
		DefaultLimit: search.DefaultLimit, MaxContextTokens: search.MaxContextTokens}
}
