package verify

import (
	"context"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// SurfacedCount reports how many times a memory has been surfaced into an
// agent's context, per the injection log. CalibrateGate uses it to tell
// surfacing from use: a memory accessed more often than it was surfaced was
// deliberately fetched at least once. Optional — nil means "usage unknown".
type SurfacedCount func(ctx context.Context, observationID string) int

// CalibrateGate turns the retrieval fixture plus injection-log history into
// labeled gate samples and sweeps the gate parameters over them. Each probe
// query runs against the live searcher; the probe's target memory is the
// on-topic label, every other hit is treated as a negative (a proxy for
// "this did not deserve injection for that query"). Scores are on the
// composite contract scale, so keyword-mode and hybrid-mode runs calibrate
// against the same numbers.
//
// The returned samples are the evidence behind the report: callers persist
// or print whichever they need.
func CalibrateGate(ctx context.Context, s Searcher, fix *RetrievalFixture, surfaced SurfacedCount) (memory.SweepReport, []memory.GateSample, error) {
	if fix == nil {
		return memory.SweepReport{}, nil, fmt.Errorf("nil fixture")
	}
	var samples []memory.GateSample
	for _, p := range fix.Probes {
		for _, q := range p.Queries {
			res, err := s.Search(ctx, memory.SearchInput{
				Query:   q,
				Project: p.Project,
				Limit:   20,
			})
			if err != nil {
				return memory.SweepReport{}, nil, fmt.Errorf("search %q: %w", q, err)
			}
			for _, r := range res {
				used := false
				if surfaced != nil {
					used = r.Observation.AccessCount > surfaced(ctx, r.Observation.ID)
				}
				samples = append(samples, memory.GateSample{
					Score:        r.Score,
					OnTopic:      r.Observation.ID == p.ID,
					CrossProject: p.Project != "" && r.Observation.Project != "" && r.Observation.Project != p.Project,
					Used:         used,
				})
			}
		}
	}
	return memory.SweepGate(samples), samples, nil
}
