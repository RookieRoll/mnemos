package verify

import (
	"context"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
)

// Searcher is the narrow surface RunRetrieval needs from memory.Service.
// Declared here at the consumer side so tests can fake it without booting
// the storage layer.
type Searcher interface {
	Search(ctx context.Context, in memory.SearchInput) ([]memory.SearchResult, error)
}

// RetrievalReport summarises a retrieval probe run.
type RetrievalReport struct {
	Cases  []CaseOutcome
	Probes []ProbeOutcome
	Total  int
	Passed int
}

// ProbeOutcome is the per-probe result. BestRank is the smallest rank
// (1-indexed) at which the target ID appeared across all queries; 0 means
// the probe never surfaced the target. Pass is BestRank in [1, ExpectInTop].
type ProbeOutcome struct {
	Probe    RetrievalProbe
	Queries  []QueryHit
	BestRank int
	Pass     bool
}

// QueryHit records what a single query produced for a probe.
type QueryHit struct {
	Query string
	Rank  int // 1-indexed; 0 means target not in returned results
}

// RunRetrieval executes every probe in the fixture against the searcher and
// returns a structured report. Pulls 3x ExpectInTop hits per query so the
// report shows near-misses (target found at rank 8 when expected ≤ 5).
func RunRetrieval(ctx context.Context, s Searcher, fix *RetrievalFixture) (*RetrievalReport, error) {
	return RunRetrievalWithDelivery(ctx, s, fix, nil)
}

// RunRetrievalWithDelivery evaluates labelled cases using the real consumer selection callback.
// A delivery expectation without a callback is an error, never an assumed empty selection.
func RunRetrievalWithDelivery(ctx context.Context, s Searcher, fix *RetrievalFixture, deliver DeliveryEvaluator) (*RetrievalReport, error) {
	if fix == nil {
		return nil, fmt.Errorf("nil fixture")
	}
	if err := validateCases(fix.Cases); err != nil {
		return nil, err
	}
	rep := &RetrievalReport{Total: len(fix.Probes) + len(fix.Cases)}
	for _, p := range fix.Probes {
		out := ProbeOutcome{Probe: p}
		limit := p.ExpectInTop * 3
		if limit < 10 {
			limit = 10
		}
		best := 0
		for _, q := range p.Queries {
			res, err := s.Search(ctx, memory.SearchInput{
				Query:   q,
				Project: p.Project,
				Limit:   limit,
			})
			if err != nil {
				return nil, fmt.Errorf("search %q: %w", q, err)
			}
			rank := 0
			for i, r := range res {
				if r.Observation.ID == p.ID {
					rank = i + 1
					break
				}
			}
			out.Queries = append(out.Queries, QueryHit{Query: q, Rank: rank})
			if rank > 0 && (best == 0 || rank < best) {
				best = rank
			}
		}
		out.BestRank = best
		out.Pass = best > 0 && best <= p.ExpectInTop
		if out.Pass {
			rep.Passed++
		}
		rep.Probes = append(rep.Probes, out)
	}
	for _, c := range fix.Cases {
		out := CaseOutcome{Case: c, Pass: c.QueryMode == "all"}
		for _, q := range c.Queries {
			res, err := s.Search(ctx, caseSearch(c, q, c.ExpectInTop*3))
			if err != nil {
				return nil, fmt.Errorf("verify: case %s search %q: %w", c.Name, q, err)
			}
			hit := QueryOutcome{Query: q, Pass: true}
			seen := make(map[string]bool)
			for i, r := range res {
				if i >= c.ExpectInTop {
					break
				}
				id := r.Observation.ID
				if seen[id] {
					continue
				}
				seen[id] = true
				switch {
				case containsID(c.RelevantIDs, id):
					hit.RelevantIDs = append(hit.RelevantIDs, id)
				case containsID(c.ForbiddenIDs, id):
					hit.ForbiddenIDs = append(hit.ForbiddenIDs, id)
				default:
					hit.UnlabelledIDs = append(hit.UnlabelledIDs, id)
				}
			}
			if len(c.RelevantIDs) > 0 {
				hit.Recall = float64(len(hit.RelevantIDs)) / float64(len(c.RelevantIDs))
			}
			hit.Pass = len(hit.RelevantIDs) == len(c.RelevantIDs) && len(hit.ForbiddenIDs) == 0
			if deliver != nil && c.ExpectNoInjection != nil {
				hit.Selected, err = deliver(ctx, c, q)
				if err != nil {
					return nil, fmt.Errorf("verify: case %s delivery: %w", c.Name, err)
				}
				hit.DeliveryMeasured = true
			} else if c.ExpectNoInjection != nil {
				return nil, fmt.Errorf("verify: case %s requires a delivery evaluator", c.Name)
			}
			if c.ExpectNoInjection != nil {
				hit.Pass = hit.Pass && (*c.ExpectNoInjection == (len(hit.Selected) == 0))
			}
			out.Queries = append(out.Queries, hit)
			if c.QueryMode == "all" {
				out.Pass = out.Pass && hit.Pass
			} else {
				out.Pass = out.Pass || hit.Pass
			}
		}
		if out.Pass {
			rep.Passed++
		}
		rep.Cases = append(rep.Cases, out)
	}
	return rep, nil
}

// DeliveryEvaluator returns final references from a configured production consumer without recording events.
type DeliveryEvaluator func(context.Context, RetrievalCase, string) ([]injection.Ref, error)

// CaseOutcome records all query variants and their explicit aggregation verdict.
type CaseOutcome struct {
	Case    RetrievalCase
	Queries []QueryOutcome
	Pass    bool
}

// QueryOutcome separates labelled recall, forbidden hits, unlabelled hits and actual delivery.
// Recall is meaningful only when the case has relevant IDs; delivery is known only when measured.
type QueryOutcome struct {
	Query                                    string
	RelevantIDs, ForbiddenIDs, UnlabelledIDs []string
	Recall                                   float64
	Selected                                 []injection.Ref
	DeliveryMeasured                         bool
	Pass                                     bool
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func caseSearch(c RetrievalCase, q string, limit int) memory.SearchInput {
	return memory.SearchInput{Query: q, Project: c.Project, AgentID: c.AgentID, IncludeRaw: c.IncludeRaw, IncludeStale: c.IncludeStale, Limit: limit}
}
