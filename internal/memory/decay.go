package memory

import (
	"math"
	"time"
)

// Ranker layers recency, access-frequency, and importance signals on top of
// the raw BM25 score returned by the store. The formula is deliberately
// simple and tunable via RankParams.
type Ranker struct {
	Params RankParams
}

// RankParams tunes the ranking formula. Defaults are calibrated for agent
// memory where observations stay relevant for weeks and importance matters
// more than recency.
type RankParams struct {
	// DecayRate controls how fast recency decays. recency_factor =
	// (1 + age_days)^(-DecayRate). 0.05 gives a gentle slope.
	DecayRate float64
	// ImportanceWeight caps how much importance can move the score. With
	// weight 0.5 and importance 1..10, the multiplier lands in [0.55, 1.0].
	ImportanceWeight float64
	// AccessBoost is the coefficient on ln(1 + access_count). 0.1 adds a
	// mild nudge for frequently-accessed memories (ACT-R base-level
	// activation analogue).
	AccessBoost float64
}

// DefaultRankParams returns sensible defaults.
func DefaultRankParams() RankParams {
	return RankParams{
		DecayRate:        0.05,
		ImportanceWeight: 0.5,
		AccessBoost:      0.10,
	}
}

// NewRanker constructs a Ranker with the given params.
func NewRanker(p RankParams) *Ranker { return &Ranker{Params: p} }

// PolicyFactor returns the non-relevance multiplier for an observation at
// time now: importance x recency x access. The composite score contract
// multiplies fused relevance by this factor; keeping it separate lets the
// result carry its own score breakdown and keeps relevance free of policy.
func (r *Ranker) PolicyFactor(o Observation, now time.Time) float64 {
	age := now.Sub(o.CreatedAt).Hours() / 24.0
	if age < 0 {
		age = 0
	}
	recency := math.Pow(1+age, -r.Params.DecayRate)

	imp := float64(o.Importance)
	if imp < 1 {
		imp = 1
	}
	if imp > 10 {
		imp = 10
	}
	importance := (1 - r.Params.ImportanceWeight) + r.Params.ImportanceWeight*(imp/10.0)

	access := 1 + r.Params.AccessBoost*math.Log1p(float64(o.AccessCount))

	return importance * recency * access
}

// Score computes the composite rank for an observation at time now.
//
//	score = base * importance_weight * recency_factor * access_factor
//
// base is whatever relevance signal the caller holds (raw BM25 in legacy
// call sites). The search path uses PolicyFactor directly against fused
// relevance instead; see the score contract in docs/ARCHITECTURE.md.
func (r *Ranker) Score(o Observation, base float64, now time.Time) float64 {
	return base * r.PolicyFactor(o, now)
}
