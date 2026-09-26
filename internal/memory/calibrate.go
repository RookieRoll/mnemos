package memory

// Gate calibration turns "what deserves injection" into a measured decision
// instead of a hand-picked constant. The sweep is deterministic and pure:
// same samples in, same parameters out. No model inference anywhere — the
// labels come from retrieval fixtures and the injection log.

// GateSample is one labeled gate decision candidate: a retrieval result the
// gate saw, whether it deserved to pass, whether it came from a different
// project than the query, and whether the agent later used it (a used
// surfacing is stronger evidence of on-topic than one nobody touched).
type GateSample struct {
	Score        float64 // composite score on the contract scale
	OnTopic      bool
	CrossProject bool
	Used         bool
}

// SweepReport is the parameter report the calibration emits: the chosen
// gate floor and project-affinity penalty on the composite score contract
// scale, plus the separation they achieved over the sample set.
type SweepReport struct {
	Floor       float64
	Penalty     float64
	BalancedAcc float64
	Samples     int
	OnTopic     int
	Used        int
}

// sweepPenalties is the candidate grid for the cross-project multiplier.
var sweepPenalties = []float64{
	0.05, 0.10, 0.15, 0.20, 0.25, 0.30, 0.35, 0.40, 0.45, 0.50,
	0.55, 0.60, 0.65, 0.70, 0.75, 0.80, 0.85, 0.90, 0.95, 1.00,
}

// SweepGate sweeps the injection floor and the project-affinity penalty
// over the labeled samples and returns the pair that maximises balanced
// accuracy ((sensitivity + specificity) / 2, so an imbalanced sample set
// cannot game the sweep by passing or dropping everything). Candidate
// floors are the distinct adjusted scores — every decision boundary is
// therefore considered. Ties resolve to the first maximum in sweep order
// (penalty ascending, floor ascending), so the result is deterministic.
//
// A sample marked Used counts double: an agent that went back to a memory
// is stronger evidence than a surfacing nobody acted on.
func SweepGate(samples []GateSample) SweepReport {
	rep := SweepReport{Samples: len(samples)}
	if len(samples) == 0 {
		return rep
	}
	weight := make([]float64, len(samples))
	var total float64
	for i, s := range samples {
		w := 1.0
		if s.Used {
			w = 2
			rep.Used++
		}
		weight[i] = w
		total += w
		if s.OnTopic {
			rep.OnTopic++
		}
	}
	if total == 0 {
		return rep
	}

	best := rep
	found := false
	adj := make([]float64, len(samples))
	for _, p := range sweepPenalties {
		for i, s := range samples {
			if s.CrossProject {
				adj[i] = s.Score * p
			} else {
				adj[i] = s.Score
			}
		}
		for _, f := range sweepFloors(adj) {
			acc := balancedAccuracy(adj, samples, weight, f)
			if !found || acc > best.BalancedAcc {
				found = true
				best.Floor, best.Penalty, best.BalancedAcc = f, p, acc
			}
		}
	}
	return best
}

// sweepFloors enumerates every decision boundary in the adjusted scores:
// the distinct values themselves (pass iff adjusted >= floor), ascending.
func sweepFloors(adj []float64) []float64 {
	seen := map[float64]bool{}
	out := make([]float64, 0, len(adj))
	for _, v := range adj {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	// insertion-sort the small slice to stay allocation-light and obvious
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// balancedAccuracy scores one candidate floor: the mean of sensitivity
// (on-topic samples passing) and specificity (off-topic samples gated),
// each weighted. When the sample set has only one class it degrades to
// plain weighted accuracy.
func balancedAccuracy(adj []float64, samples []GateSample, weight []float64, floor float64) float64 {
	tpW, tpTotal, tnW, tnTotal := 0.0, 0.0, 0.0, 0.0
	for i, s := range samples {
		passes := adj[i] >= floor
		if s.OnTopic {
			tpTotal += weight[i]
			if passes {
				tpW += weight[i]
			}
		} else {
			tnTotal += weight[i]
			if !passes {
				tnW += weight[i]
			}
		}
	}
	switch {
	case tpTotal == 0:
		return tnW / tnTotal
	case tnTotal == 0:
		return tpW / tpTotal
	default:
		return 0.5 * (tpW/tpTotal + tnW/tnTotal)
	}
}
