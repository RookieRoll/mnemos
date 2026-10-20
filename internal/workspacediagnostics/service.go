package workspacediagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Service compares explicit metadata under bounded, deterministic rules.
type Service struct{ cfg Config }

// NewService validates the required dependencies and scan budgets.
func NewService(cfg Config) (*Service, error) {
	if cfg.Reader == nil || cfg.Probe == nil || cfg.MaxRoots < 1 || cfg.MaxCandidates < 1 || cfg.Timeout <= 0 {
		return nil, fmt.Errorf("workspace diagnostics: reader, probe and positive budgets required")
	}
	return &Service{cfg: cfg}, nil
}

// Diagnose produces report-only advice; no finding certifies a safe migration.
func (s *Service) Diagnose(ctx context.Context) (Report, error) {
	out := Report{ProtocolVersion: 1, Status: "ok", ReadOnly: true,
		MaxRoots: s.cfg.MaxRoots, MaxCandidates: s.cfg.MaxCandidates,
		Reasons: []string{}, Roots: []Root{}, Candidates: []Candidate{}}
	bounded, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	snapshot, err := s.cfg.Reader.Snapshot(bounded, s.cfg.MaxRoots)
	if ctx.Err() != nil {
		return out, fmt.Errorf("workspace diagnostics: canceled: %w", ctx.Err())
	}
	if bounded.Err() != nil {
		out.incomplete("timeout_during_metadata_snapshot")
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("workspace diagnostics: read metadata: %w", err)
	}
	out.RootCountKnown = true
	out.TotalRoots = snapshot.TotalRoots
	out.PossiblePairs = int64(snapshot.TotalRoots) * int64(snapshot.TotalRoots-1) / 2
	if !snapshot.Exists {
		out.Status = "no_history"
		return out, nil
	}
	if snapshot.TotalRoots > len(snapshot.Roots) {
		out.incomplete("max_roots")
	}
	if snapshot.RetryOverlapTruncated {
		out.incomplete("retry_overlap_budget")
	}
	sort.Slice(snapshot.Roots, func(i, j int) bool { return identityLess(snapshot.Roots[i].Identity, snapshot.Roots[j].Identity) })
	for _, metadata := range snapshot.Roots {
		if ctx.Err() != nil {
			return out, fmt.Errorf("workspace diagnostics: canceled: %w", ctx.Err())
		}
		if bounded.Err() != nil {
			out.incomplete("timeout_during_path_probes")
			break
		}
		state := s.cfg.Probe.Inspect(bounded, metadata.Root)
		out.Roots = append(out.Roots, Root{Metadata: metadata, Path: state})
		if ctx.Err() != nil {
			return out, fmt.Errorf("workspace diagnostics: canceled: %w", ctx.Err())
		}
		if bounded.Err() != nil {
			out.incomplete("timeout_during_path_probes")
			break
		}
		if state.Status == "unknown" || (state.Status == "live" && state.Git == "unknown") {
			out.incomplete("inaccessible_or_unknown_metadata")
		}
	}
	out.ScannedRoots = len(out.Roots)
	overlaps := make(map[pairKey]int64, len(snapshot.RetryOverlap))
	for _, overlap := range snapshot.RetryOverlap {
		overlaps[newPairKey(overlap.Left, overlap.Right)] += overlap.Count
	}
	for i := range out.Roots {
		for j := i + 1; j < len(out.Roots); j++ {
			if ctx.Err() != nil {
				return out, fmt.Errorf("workspace diagnostics: canceled: %w", ctx.Err())
			}
			if bounded.Err() != nil {
				out.incomplete("timeout_during_comparisons")
				return out, nil
			}
			out.ComparedPairs++
			candidate, related := compare(out.Roots[i], out.Roots[j])
			if !related {
				continue
			}
			if len(out.Candidates) >= s.cfg.MaxCandidates {
				out.incomplete("max_candidates")
				return out, nil
			}
			candidate.RetryOverlap = overlaps[newPairKey(candidate.Left.Identity, candidate.Right.Identity)]
			candidate.RetryOverlapKnown = !snapshot.RetryOverlapTruncated || candidate.RetryOverlap > 0
			if candidate.RetryOverlap > 0 {
				candidate.Blockers = append(candidate.Blockers, "overlapping_startup_retry_identities")
			} else if !candidate.RetryOverlapKnown {
				candidate.Blockers = append(candidate.Blockers, "retry_overlap_unknown_due_to_budget")
			}
			if candidate.Left.Counts.OwnerCheckpoints+candidate.Right.Counts.OwnerCheckpoints > 0 || candidate.Left.Counts.WorkspaceCheckpoints+candidate.Right.Counts.WorkspaceCheckpoints > 0 {
				candidate.Blockers = append(candidate.Blockers, "checkpoint_visibility_review_required")
			}
			out.Candidates = append(out.Candidates, candidate)
		}
	}
	return out, nil
}

type pairKey struct{ left, right Identity }

func newPairKey(a, b Identity) pairKey {
	if identityLess(b, a) {
		a, b = b, a
	}
	return pairKey{left: a, right: b}
}

func (r *Report) incomplete(reason string) {
	r.Partial = true
	r.Status = "partial"
	for _, old := range r.Reasons {
		if old == reason {
			return
		}
	}
	r.Reasons = append(r.Reasons, reason)
}

func compare(a, b Root) (Candidate, bool) {
	c := Candidate{Left: a.Metadata, Right: b.Metadata, Evidence: []string{},
		Blockers:    []string{"historical_identity_not_verified", "workspace_scope_and_visibility_change_requires_review"},
		MergeSafety: "not_established"}
	if a.Counts.OpenSessions+b.Counts.OpenSessions > 0 {
		c.Blockers = append(c.Blockers, "active_sessions")
	}
	if a.Counts.OrphanSessions+b.Counts.OrphanSessions > 0 {
		c.Blockers = append(c.Blockers, "orphan_session_bindings")
	}
	live := a.Path.Status == "live" && b.Path.Status == "live"
	physical := live && ((a.Path.Canonical != "" && a.Path.Canonical == b.Path.Canonical) || (a.Path.Info != nil && b.Path.Info != nil && os.SameFile(a.Path.Info, b.Path.Info)))
	if physical {
		c.Classification, c.Consolidation = "verified_path_equivalence", true
		c.Evidence = append(c.Evidence, "existing_paths_resolve_to_same_physical_directory")
		return c, true
	}
	ap, bp := a.Root, b.Root
	if a.Path.Canonical != "" {
		ap = a.Path.Canonical
	}
	if b.Path.Canonical != "" {
		bp = b.Path.Canonical
	}
	nested := strictlyContains(ap, bp) || strictlyContains(bp, ap)
	similar := similarName(ap, bp)
	if !nested && !similar && a.Root != b.Root && a.Key != b.Key {
		return Candidate{}, false
	}
	if a.Path.Status == "unknown" || b.Path.Status == "unknown" || (live && (a.Path.Git == "unknown" || b.Path.Git == "unknown")) {
		c.Classification = "unknown_evidence"
		c.Evidence = append(c.Evidence, "related_spelling_with_incomplete_local_metadata")
		c.Blockers = append(c.Blockers, "inaccessible_or_unknown_metadata")
		return c, true
	}
	if live && (a.Path.Git == "git" || b.Path.Git == "git") {
		c.Classification = "independent_git_roots"
		if a.Path.Git == "git" && b.Path.Git == "git" && a.Path.GitRoot == b.Path.GitRoot {
			c.Evidence = append(c.Evidence, "same_git_worktree_does_not_establish_historical_boundary")
		} else {
			c.Evidence = append(c.Evidence, "distinct_git_or_project_boundaries_keep_separate")
		}
		c.Blockers = append(c.Blockers, "keep_independent_by_default")
		return c, true
	}
	if live && nested && a.Path.Git == "non_git" && b.Path.Git == "non_git" {
		c.Classification, c.Consolidation = "possible_boundary_split", true
		c.Evidence = append(c.Evidence, "recorded_non_git_root_contains_another_recorded_root")
		c.Blockers = append(c.Blockers, "modules_or_independent_projects_unknown")
		return c, true
	}
	if similar && ((a.Path.Status == "missing" && b.Path.Status == "live") || (b.Path.Status == "missing" && a.Path.Status == "live")) {
		c.Classification, c.Consolidation = "possible_migration", true
		c.Evidence = append(c.Evidence, "missing_historical_path_and_similarly_named_live_path")
		c.Blockers = append(c.Blockers, "move_not_verified")
		return c, true
	}
	c.Classification = "weak_similarity"
	c.Evidence = append(c.Evidence, "lexical_or_identity_metadata_similarity_only")
	return c, true
}

func identityLess(a, b Identity) bool {
	return a.Root < b.Root || a.Root == b.Root && a.Key < b.Key
}

func strictlyContains(parent, child string) bool {
	if parent == "" || child == "" || parent == child || !filepath.IsAbs(parent) || !filepath.IsAbs(child) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func similarName(a, b string) bool {
	a, b = strings.ToLower(filepath.Base(a)), strings.ToLower(filepath.Base(b))
	if a == b && a != "." && a != string(filepath.Separator) {
		return true
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 4 && (strings.HasPrefix(b, a+"-") || strings.HasPrefix(b, a+"_") || strings.HasPrefix(b, a+" "))
}
