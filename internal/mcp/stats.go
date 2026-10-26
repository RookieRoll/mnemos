package mcp

import (
	"context"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// statsView renders the typed statistics value into the wire shape published by
// both MCP statistics surfaces: the mnemos://stats resource (resources.go
// readStats) and the mnemos_stats tool (tools.go handleStats).
//
// This is the only place a statistic becomes output. Both surfaces call it and
// neither re-derives a number, so the two cannot drift; TestMCPSurfaces_StatsAgree
// asserts the rendered documents are identical for one fixture. In particular
// storage_bytes is the value the storage layer computed for the main database
// file plus its write-ahead log, which is also the number mnemos stats prints.
//
// Session backlog reporting (open session count and oldest age) is reserved on
// this map: it will be added here, once, and both surfaces inherit it. Until the
// session service exposes it, the key set is exactly what it is today.
func (s *Server) statsView(ctx context.Context, st memory.Stats) map[string]any {
	out := map[string]any{
		"observations":      st.Observations,
		"live_observations": st.LiveObservations,
		"sessions":          st.Sessions,
		"skills":            st.Skills,
		"storage_bytes":     st.StorageBytes,
		"top_tags":          st.TopTags,
		"recent_sessions":   st.RecentSessions,
		"embedding":         map[string]any{"enabled": s.cfg.Memory.HybridEnabled()},
	}
	if s.cfg.Rumination != nil {
		if c, err := s.cfg.Rumination.Counts(ctx); err == nil {
			out["rumination"] = map[string]any{
				"pending":   c.Pending,
				"resolved":  c.Resolved,
				"dismissed": c.Dismissed,
			}
		}
	}
	return out
}
