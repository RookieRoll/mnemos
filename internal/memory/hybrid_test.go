package memory

import (
	"math"
	"testing"
)

func TestCosineOrthogonalIsZero(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	if c := cosine(a, b); c != 0 {
		t.Errorf("orthogonal cosine should be 0, got %v", c)
	}
}

func TestCosineIdenticalIsOne(t *testing.T) {
	a := []float32{0.3, 0.4, 0.5}
	if c := cosine(a, a); math.Abs(c-1) > 1e-6 {
		t.Errorf("identical cosine should be 1, got %v", c)
	}
}

func TestCosineLengthMismatchReturnsZero(t *testing.T) {
	if c := cosine([]float32{1}, []float32{1, 0}); c != 0 {
		t.Errorf("mismatched lengths should return 0, got %v", c)
	}
	if c := cosine(nil, []float32{1}); c != 0 {
		t.Errorf("nil operand should return 0")
	}
}

func TestNormBM25Transform(t *testing.T) {
	cases := []struct {
		name string
		b, k float64
		want float64
	}{
		{"negative magnitude is no signal", -5, 8, 0},
		{"zero magnitude is no signal", 0, 8, 0},
		{"zero constant degrades to no signal", 5, 0, 0},
		{"saturates at half the constant", 8, 8, 0.5},
		{"weak match normalises low", 2, 8, 0.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normBM25(tc.b, tc.k); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("normBM25(%v, %v) = %v, want %v", tc.b, tc.k, got, tc.want)
			}
		})
	}
	if normBM25(1000, 8) <= normBM25(10, 8) {
		t.Error("transform must stay monotone in the magnitude")
	}
	if normBM25(1000, 8) >= 1 {
		t.Error("transform must stay below 1")
	}
}

func TestNormCosClipping(t *testing.T) {
	cases := []struct {
		name     string
		c, lo, hi float64
		want     float64
	}{
		{"below window", 0.05, 0.2, 0.8, 0},
		{"at low edge", 0.2, 0.2, 0.8, 0},
		{"mid window", 0.5, 0.2, 0.8, 0.5},
		{"at high edge", 0.8, 0.2, 0.8, 1},
		{"above window", 0.95, 0.2, 0.8, 1},
		{"degenerate window is no signal", 0.5, 0.5, 0.5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normCos(tc.c, tc.lo, tc.hi); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("normCos(%v, %v, %v) = %v, want %v", tc.c, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}

func TestCombineRelevanceRenormalises(t *testing.T) {
	bm25, cos := 0.8, 0.2
	cases := []struct {
		name      string
		nBM25     *float64
		nCos      *float64
		alpha     float64
		want      float64
	}{
		{"keyword signal alone wins outright", &bm25, nil, 0.5, 0.8},
		{"vector signal alone wins outright", nil, &cos, 0.5, 0.2},
		{"both signals average by weight", &bm25, &cos, 0.5, 0.5},
		{"alpha tilts toward keyword", &bm25, &cos, 1.0, 0.8},
		{"pure-vector config ignores keyword", &bm25, &cos, 0.0, 0.2},
		{"no signals is zero relevance", nil, nil, 0.5, 0},
		{"pure-keyword config drops vector-only hit", nil, &cos, 1.0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := combineRelevance(tc.nBM25, tc.nCos, tc.alpha)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("combineRelevance = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVecHasSignal(t *testing.T) {
	if vecHasSignal(nil) {
		t.Error("nil vector has no signal")
	}
	if vecHasSignal([]float32{0, 0, 0}) {
		t.Error("zero-norm vector must count as no signal, same as a missing embedding")
	}
	if !vecHasSignal([]float32{0, 0.1, 0}) {
		t.Error("non-zero vector has signal")
	}
}

func TestFillDefaultsPerField(t *testing.T) {
	p := HybridParams{Alpha: 1.0}.fillDefaults()
	d := DefaultHybridParams()
	if p.Alpha != 1.0 {
		t.Errorf("Alpha must be preserved, got %v", p.Alpha)
	}
	if p.BM25K != d.BM25K {
		t.Errorf("BM25K defaulted to %v, want %v", p.BM25K, d.BM25K)
	}
	if p.CosLow != d.CosLow || p.CosHigh != d.CosHigh {
		t.Errorf("cos window defaulted to [%v,%v], want [%v,%v]", p.CosLow, p.CosHigh, d.CosLow, d.CosHigh)
	}
}
