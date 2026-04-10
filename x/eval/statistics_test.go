package eval_test

import (
	"math"
	"testing"

	"github.com/nijaru/canto/x/eval"
)

func TestStatisticsSeries(t *testing.T) {
	results := []eval.EvalResult{
		{
			RunID:     "run-1",
			TurnCount: 1,
			Scores:    map[string]float64{"reliability": 1.0},
		},
		{
			RunID:     "run-2",
			TurnCount: 1,
			Scores:    map[string]float64{"reliability": 0.0},
		},
		{
			RunID:     "run-3",
			TurnCount: 3,
			Scores:    map[string]float64{"reliability": 1.0},
		},
	}

	series, err := eval.NewRunSeries(results, "reliability")
	if err != nil {
		t.Fatalf("NewRunSeries: %v", err)
	}
	if got, want := len(series.Samples), 3; got != want {
		t.Fatalf("sample count: got %d want %d", got, want)
	}

	gds, err := eval.GDS(results, "reliability")
	if err != nil {
		t.Fatalf("GDS: %v", err)
	}
	if diff := math.Abs(gds - 0.8); diff > 1e-9 {
		t.Fatalf("GDS: got %.12f want 0.8", gds)
	}

	vaf, err := eval.VAF(results, "reliability")
	if err != nil {
		t.Fatalf("VAF: %v", err)
	}
	if diff := math.Abs(vaf - 0.3333333333333333); diff > 1e-9 {
		t.Fatalf("VAF: got %.12f want 0.3333333333333333", vaf)
	}

	mop, err := eval.MOP(results, "reliability")
	if err != nil {
		t.Fatalf("MOP: %v", err)
	}
	if diff := math.Abs(mop - 1.0); diff > 1e-9 {
		t.Fatalf("MOP: got %.12f want 1.0", mop)
	}
}

func TestStatisticsMOPCollapseOnset(t *testing.T) {
	results := []eval.EvalResult{
		{RunID: "run-1", Scores: map[string]float64{"reliability": 0.9}},
		{RunID: "run-2", Scores: map[string]float64{"reliability": 0.85}},
		{RunID: "run-3", Scores: map[string]float64{"reliability": 0.2}},
		{RunID: "run-4", Scores: map[string]float64{"reliability": 0.1}},
	}

	series, err := eval.NewRunSeries(results, "reliability")
	if err != nil {
		t.Fatalf("NewRunSeries: %v", err)
	}
	if got, want := series.MOP(), 2.0/3.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("MOP: got %.12f want %.12f", got, want)
	}
}

func TestStatisticsMissingScore(t *testing.T) {
	_, err := eval.NewRunSeries([]eval.EvalResult{{
		RunID:  "run-1",
		Scores: map[string]float64{"other": 1.0},
	}}, "reliability")
	if err == nil {
		t.Fatal("expected missing score error")
	}
}
