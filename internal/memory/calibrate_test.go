package memory

import (
	"reflect"
	"testing"
)

func TestSweepGateSeparatesClasses(t *testing.T) {
	samples := []GateSample{
		{Score: 0.85, OnTopic: true},
		{Score: 0.60, OnTopic: true},
		{Score: 0.50, OnTopic: true},
		{Score: 0.25, OnTopic: false},
		{Score: 0.12, OnTopic: false},
		{Score: 0.05, OnTopic: false},
	}
	rep := SweepGate(samples)
	if rep.BalancedAcc != 1.0 {
		t.Errorf("clean separation should sweep to 1.0, got %v", rep.BalancedAcc)
	}
	if rep.Floor <= 0.25 || rep.Floor > 0.50 {
		t.Errorf("floor %v should land in the gap (0.25, 0.50]", rep.Floor)
	}
	if rep.Samples != 6 || rep.OnTopic != 3 {
		t.Errorf("counts wrong: %+v", rep)
	}
}

func TestSweepGatePenalisesCrossProjectNoise(t *testing.T) {
	// Cross-project noise sits close to on-topic scores; the sweep must
	// choose a real penalty rather than 1.0 (no penalty) to separate them.
	samples := []GateSample{
		{Score: 0.80, OnTopic: true},
		{Score: 0.70, OnTopic: true},
		{Score: 0.75, OnTopic: false, CrossProject: true},
		{Score: 0.65, OnTopic: false, CrossProject: true},
		{Score: 0.20, OnTopic: false},
	}
	rep := SweepGate(samples)
	if rep.BalancedAcc != 1.0 {
		t.Errorf("penalty should make classes separable, got acc %v (floor %v penalty %v)",
			rep.BalancedAcc, rep.Floor, rep.Penalty)
	}
	if rep.Penalty >= 1.0 {
		t.Errorf("penalty %v should be below 1.0 to crush cross-project noise", rep.Penalty)
	}
}

func TestSweepGateUsedSamplesWeighDouble(t *testing.T) {
	// One used on-topic sample far below two unused on-topic samples:
	// with double weight it drags the floor down to include it.
	samples := []GateSample{
		{Score: 0.9, OnTopic: true},
		{Score: 0.8, OnTopic: true},
		{Score: 0.3, OnTopic: true, Used: true},
		{Score: 0.1, OnTopic: false},
	}
	rep := SweepGate(samples)
	if rep.Used != 1 {
		t.Errorf("used count = %d, want 1", rep.Used)
	}
	if rep.Floor > 0.3 {
		t.Errorf("floor %v should admit the used sample at 0.3", rep.Floor)
	}
}

func TestSweepGateDeterministic(t *testing.T) {
	samples := []GateSample{
		{Score: 0.7, OnTopic: true, CrossProject: true},
		{Score: 0.55, OnTopic: false},
		{Score: 0.35, OnTopic: true},
		{Score: 0.30, OnTopic: false, CrossProject: true, Used: true},
	}
	if a, b := SweepGate(samples), SweepGate(samples); !reflect.DeepEqual(a, b) {
		t.Errorf("sweep must be deterministic: %+v vs %+v", a, b)
	}
}

func TestSweepGateEmptyInput(t *testing.T) {
	if rep := SweepGate(nil); rep.Samples != 0 || rep.Floor != 0 || rep.Penalty != 0 {
		t.Errorf("empty input should yield a zero report, got %+v", rep)
	}
}

func TestSweepGateSingleClass(t *testing.T) {
	// Only negatives: the sweep still emits a usable floor and score.
	rep := SweepGate([]GateSample{{Score: 0.4}, {Score: 0.2}, {Score: 0.1}})
	if rep.BalancedAcc <= 0 || rep.Floor <= 0 {
		t.Errorf("single-class sweep should still report, got %+v", rep)
	}
}
