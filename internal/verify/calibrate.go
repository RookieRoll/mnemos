package verify

import (
	"context"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// SurfacedCount reports how many times a memory has been surfaced into an
// agent's context, per the injection log. CalibrateGate uses it to tell
// surfacing from use: access exceeding surfacing is a compatibility usage proxy only; it does
// not establish deliberate use or success. Optional — nil means "usage unknown".
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
	if fix != nil && len(fix.Cases) > 0 {
		return memory.SweepReport{}, nil, fmt.Errorf("verify: explicit cases require CalibrateLabelledGate; legacy proxy labels must remain separate")
	}
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

// CalibrationCoverage states the labelled evidence available to a sweep.
// SeparationMeasured is false for empty and single-class samples.
type CalibrationCoverage struct {
	Positive, Negative, Unlabelled, Queries int
	SeparationMeasured                      bool
}

// LabelledCalibration keeps explicit labels separate from legacy proxy results.
type LabelledCalibration struct {
	Basis    string
	Sweep    memory.SweepReport
	Coverage CalibrationCoverage
	Samples  []memory.GateSample
	Queries  []CalibrationQuery
}

// CalibrationQuery identifies per-query label coverage.
type CalibrationQuery struct {
	Case, Query                    string
	Positive, Negative, Unlabelled int
}

// CalibrateLabelledGate excludes unlabelled hits and does not infer success from access counts.
func CalibrateLabelledGate(ctx context.Context, s Searcher, fix *RetrievalFixture) (LabelledCalibration, error) {
	rep := LabelledCalibration{Basis: "explicit relevance/forbidden labels; no causal outcome attribution"}
	if fix == nil {
		return rep, fmt.Errorf("verify: nil fixture")
	}
	if err := validateCases(fix.Cases); err != nil {
		return rep, err
	}
	for _, c := range fix.Cases {
		for _, q := range c.Queries {
			hits, err := s.Search(ctx, caseSearch(c, q, 20))
			if err != nil {
				return rep, fmt.Errorf("verify: calibrate %q: %w", q, err)
			}
			qr := CalibrationQuery{Case: c.Name, Query: q}
			seen := make(map[string]bool)
			for _, h := range hits {
				id := h.Observation.ID
				if seen[id] {
					continue
				}
				seen[id] = true
				positive, negative := containsID(c.RelevantIDs, id), containsID(c.ForbiddenIDs, id)
				if !positive && !negative {
					qr.Unlabelled++
					continue
				}
				if positive {
					qr.Positive++
				} else {
					qr.Negative++
				}
				rep.Samples = append(rep.Samples, memory.GateSample{Score: h.Score, OnTopic: positive, CrossProject: c.Project != "" && h.Observation.Project != "" && h.Observation.Project != c.Project})
			}
			rep.Queries = append(rep.Queries, qr)
			rep.Coverage.Queries++
			rep.Coverage.Positive += qr.Positive
			rep.Coverage.Negative += qr.Negative
			rep.Coverage.Unlabelled += qr.Unlabelled
		}
	}
	rep.Coverage.SeparationMeasured = rep.Coverage.Positive > 0 && rep.Coverage.Negative > 0
	rep.Sweep = memory.SweepGate(rep.Samples)
	return rep, nil
}
