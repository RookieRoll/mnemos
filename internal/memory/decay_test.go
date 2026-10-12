package memory

import (
	"math"
	"testing"
	"time"
)

func TestImportanceMovesScore(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	low := Observation{Importance: 1, CreatedAt: now}
	high := Observation{Importance: 10, CreatedAt: now}
	if r.Score(high, 1.0, now) <= r.Score(low, 1.0, now) {
		t.Errorf("high importance should outrank low")
	}
}

func TestAccessCountBoostsScore(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	cold := Observation{Importance: 5, CreatedAt: now, AccessCount: 0}
	hot := Observation{Importance: 5, CreatedAt: now, AccessCount: 100}
	if r.Score(hot, 1.0, now) <= r.Score(cold, 1.0, now) {
		t.Errorf("frequently-accessed should outrank cold")
	}
}

func TestImportanceClampedOutOfRange(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	// Importance 0 or 11 should clamp into [1, 10].
	neg := Observation{Importance: 0, CreatedAt: now}
	over := Observation{Importance: 11, CreatedAt: now}
	if r.Score(neg, 1.0, now) == 0 || math.IsNaN(r.Score(over, 1.0, now)) {
		t.Errorf("out-of-range importance should not produce zero/NaN")
	}
}

func TestDecayAtFutureCreatedAtTreatedAsZero(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	future := Observation{Importance: 5, CreatedAt: now.Add(time.Hour)}
	present := Observation{Importance: 5, CreatedAt: now}
	// Should be equal — future age clamps to 0.
	if math.Abs(r.Score(future, 1.0, now)-r.Score(present, 1.0, now)) > 1e-6 {
		t.Errorf("future createdAt should clamp age to 0")
	}
}

func TestHashContentStableAcrossWhitespace(t *testing.T) {
	h1 := hashContent(TypeDecision, "Use X", "we use X")
	h2 := hashContent(TypeDecision, "  Use X  ", "we use X\n")
	if h1 != h2 {
		t.Errorf("whitespace should not change hash")
	}
}

func TestHashContentSensitiveToType(t *testing.T) {
	h1 := hashContent(TypeDecision, "Use X", "we use X")
	h2 := hashContent(TypeBugfix, "Use X", "we use X")
	if h1 == h2 {
		t.Errorf("different types should hash differently")
	}
}

func TestDecayRateFor_AllTypes(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	expected := map[ObsType]float64{
		TypeConvention:   0.002,
		TypeArchitecture: 0.002,
		TypeDecision:     0.002,
		TypePattern:      0.030,
		TypePreference:   0.030,
		TypeSemantic:     0.030,
		TypeBugfix:       0.050,
		TypeContext:      0.050,
		TypeEpisodic:     0.050,
		TypeDream:        0.050,
		TypeCorrection:   0.100,
		TypeProcedural:   0.050,
	}
	for _, typ := range AllTypes {
		want := expected[typ]
		if want == 0 {
			want = 0.050
		}
		got := r.DecayRateFor(typ)
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("type %q: got decay rate %v, want %v", typ, got, want)
		}
	}

	// Custom override in params is respected.
	custom := NewRanker(RankParams{DecayRate: 0.08, ImportanceWeight: 0.5, AccessBoost: 0.1})
	for _, typ := range AllTypes {
		if got := custom.DecayRateFor(typ); got != 0.08 {
			t.Errorf("custom params should override table: got %v, want 0.08", got)
		}
	}
}

func TestPinnedBypassesDecay(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -180)

	pinned := Observation{Importance: 5, CreatedAt: oldTime, Pinned: true, Type: TypeCorrection}
	fresh := Observation{Importance: 5, CreatedAt: now, Pinned: false, Type: TypeCorrection}

	scorePinned := r.Score(pinned, 1.0, now)
	scoreFresh := r.Score(fresh, 1.0, now)

	if math.Abs(scorePinned-scoreFresh) > 1e-6 {
		t.Errorf("pinned 180-day observation should match fresh: pinned=%v, fresh=%v", scorePinned, scoreFresh)
	}
}

func TestTypeDifferentiatedDecayRates(t *testing.T) {
	r := NewRanker(DefaultRankParams())
	now := time.Now().UTC()

	// 1. Convention at 60 days maintains > 0.99 recency
	t60 := now.AddDate(0, 0, -60)
	conv := Observation{Importance: 10, CreatedAt: t60, Type: TypeConvention}
	convFactor := r.PolicyFactor(conv, now)
	// For importance 10 (multiplier 1.0) and access 0 (multiplier 1.0), factor = recency
	if convFactor < 0.99 {
		t.Errorf("convention at 60 days should keep recency factor > 0.99, got %v", convFactor)
	}

	// 2. Correction at 14 days drops below 0.77 recency (15^-0.10 ≈ 0.763)
	t14 := now.AddDate(0, 0, -14)
	corr := Observation{Importance: 10, CreatedAt: t14, Type: TypeCorrection}
	corrFactor := r.PolicyFactor(corr, now)
	if corrFactor >= 0.77 {
		t.Errorf("correction at 14 days should decay below 0.77, got %v", corrFactor)
	}

	// 3. At 60 days, durable types outrank standard and correction types
	bug := Observation{Importance: 10, CreatedAt: t60, Type: TypeBugfix}
	bugFactor := r.PolicyFactor(bug, now)
	if convFactor <= bugFactor {
		t.Errorf("convention (%v) must outrank bugfix (%v) at 60 days", convFactor, bugFactor)
	}
}
