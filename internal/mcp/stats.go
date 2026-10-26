package mcp

import (
	"context"
	"time"

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
// The open-session backlog is rendered here too, from the one session-service
// read the CLI stats command uses, so the reported count and oldest start
// instant are the same value in all three views.
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
	if backlog, ok := s.openSessionsView(ctx); ok {
		out["open_sessions"] = backlog
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

// openSessionsView renders the open-session backlog — how many sessions were
// never closed, and when the oldest of them started — from the same single
// session-service read behind the CLI line 'open sessions: N (oldest Xd)'
// (session.Service.OpenBacklog). Publishing the count and the start instant
// rather than a pre-formatted age string keeps the surfaces machine-readable
// while deriving both numbers from one read, so the CLI, the mnemos://stats
// resource and the mnemos_stats tool cannot disagree about either of them.
//
// The second result is false when there is nothing to render: no session
// service is wired, or the backlog read failed. The key is then omitted and the
// rest of the statistics still render, matching how the optional rumination
// counts behave. A zero would claim "no open sessions" instead of "unknown",
// which is a worse lie than an absent field in a diagnostics document.
func (s *Server) openSessionsView(ctx context.Context) (map[string]any, bool) {
	if s.cfg.Sessions == nil {
		return nil, false
	}
	backlog, err := s.cfg.Sessions.OpenBacklog(ctx)
	if err != nil {
		s.cfg.Logger.Debug("stats: read open session backlog", "error", err)
		return nil, false
	}
	// An empty backlog has no oldest session; null says exactly that, while a
	// zero time would serialize as year one and read like a very old session.
	var oldest *time.Time
	if !backlog.OldestStartedAt.IsZero() {
		at := backlog.OldestStartedAt.UTC()
		oldest = &at
	}
	return map[string]any{
		"count":             backlog.Count,
		"oldest_started_at": oldest,
	}, true
}
