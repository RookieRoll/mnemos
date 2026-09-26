package memory

import "math"

// HybridParams tunes how recall-source signals fuse into the relevance
// component of the composite score contract (see docs/ARCHITECTURE.md and
// the memory-retrieval spec). Relevance is the weighted mean of per-source
// absolute relevances in [0,1]: Alpha weighs the keyword signal,
// (1-Alpha) the vector signal. The 2026 LongMemEval research says the
// sweet spot is near 0.5 — keyword catches exact identifiers, cosine
// catches paraphrases.
type HybridParams struct {
	// Alpha is the keyword weight in the relevance mean (0..1, default 0.5).
	// 1.0 is pure keyword, 0.0 pure semantic. Unlike the other fields, 0 is
	// a legal setting here, not an "unset" marker, so it never defaults.
	Alpha float64
	// BM25K is the saturation constant of the keyword transform
	// n = b/(b+k): a raw BM25 magnitude of k normalises to relevance 0.5.
	// Default 8, in the band of a corpus's typical top-hit magnitude.
	BM25K float64
	// CosLow and CosHigh clip raw cosine similarity into [0,1]: below
	// CosLow a vector match carries no relevance, at CosHigh it saturates.
	// Defaults 0.2/0.8 bracket typical sentence-embedding similarities.
	CosLow  float64
	CosHigh float64
}

// ScoreCeiling is the documented upper bound of the composite score
// contract: Score = relevance x policy, with relevance in [0,1] and policy
// factors around 1. Scores clamp here so the bound is a real invariant of
// the contract rather than a hope about the inputs.
const ScoreCeiling = 1.2

// DefaultHybridParams returns literature-backed defaults.
func DefaultHybridParams() HybridParams {
	return HybridParams{Alpha: 0.5, BM25K: 8.0, CosLow: 0.2, CosHigh: 0.8}
}

// fillDefaults replaces unset transform parameters with their defaults,
// field by field. A caller that sets only Alpha must not end up with a zero
// saturation constant silently zeroing the keyword signal. Alpha is left
// alone: 0 is a legal pure-vector setting.
func (p HybridParams) fillDefaults() HybridParams {
	d := DefaultHybridParams()
	if p.BM25K <= 0 {
		p.BM25K = d.BM25K
	}
	if p.CosHigh <= p.CosLow {
		p.CosLow, p.CosHigh = d.CosLow, d.CosHigh
	}
	return p
}

// normBM25 maps a raw BM25 magnitude to absolute relevance in [0,1) via a
// saturating transform. Unlike a rank, the value keeps the magnitude
// information a consumer gate needs: a weak result set normalises low even
// for its top hit.
func normBM25(b, k float64) float64 {
	if b <= 0 || k <= 0 {
		return 0
	}
	return b / (b + k)
}

// normCos clips raw cosine similarity into [0,1]. The window [lo, hi] is
// calibrated so incidental similarity lands below lo and saturating
// agreement reaches 1. A degenerate window yields 0 (no signal).
func normCos(c, lo, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	n := (c - lo) / (hi - lo)
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

// combineRelevance returns the weighted mean of the available per-source
// relevances. Weights renormalise over the signals actually present, so a
// memory without an embedding is not penalised for the missing signal.
// A nil pointer means "this source has no signal for this candidate";
// with no signals at all the relevance is 0.
func combineRelevance(nBM25, nCos *float64, alpha float64) float64 {
	var num, den float64
	if nBM25 != nil {
		num += alpha * *nBM25
		den += alpha
	}
	if nCos != nil {
		num += (1 - alpha) * *nCos
		den += 1 - alpha
	}
	if den <= 0 {
		return 0
	}
	return num / den
}

// vecHasSignal reports whether an embedding carries a usable signal. A
// zero-norm vector (a provider bug or a partial write) computes as cosine 0
// and would silently penalise its row versus rows with no embedding at all;
// treating it as "no signal" keeps the two cases identical.
func vecHasSignal(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return true
		}
	}
	return false
}

// cosine computes the cosine similarity between two float32 vectors. Zero
// length or mismatched lengths return 0 so callers can treat "no vector"
// as "no signal" without branches in the hot path.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
