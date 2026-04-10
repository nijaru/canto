package eval

import (
	"fmt"
	"math"
)

const (
	defaultMOPCollapseThreshold = 0.35
	defaultMOPCollapseWindow    = 2
)

// RunSample captures the per-run signal for a named score series.
type RunSample struct {
	RunID     string
	Score     float64
	TurnCount int
	TotalCost float64
}

// RunSeries is a reusable sequence of scored runs for one score key.
type RunSeries struct {
	Key     string
	Samples []RunSample
}

// NewRunSeries extracts one named score series from the provided results.
func NewRunSeries(results []EvalResult, key string) (RunSeries, error) {
	series := RunSeries{
		Key:     key,
		Samples: make([]RunSample, 0, len(results)),
	}
	for _, res := range results {
		score, ok := res.Scores[key]
		if !ok {
			return RunSeries{}, fmt.Errorf("eval: run %q missing score %q", res.RunID, key)
		}
		series.Samples = append(series.Samples, RunSample{
			RunID:     res.RunID,
			Score:     score,
			TurnCount: res.TurnCount,
			TotalCost: res.TotalCost,
		})
	}
	return series, nil
}

// GDS computes a weighted partial-credit summary over repeated runs.
//
// The default weighting uses turn count as a cheap proxy for how much work
// each run represented. Longer runs should contribute more to the aggregate
// than one-turn stubs.
func (s RunSeries) GDS() float64 {
	if len(s.Samples) == 0 {
		return 0
	}

	var weightedSum float64
	var totalWeight float64
	for _, sample := range s.Samples {
		weight := math.Max(1, float64(sample.TurnCount))
		weightedSum += sample.Score * weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return 0
	}
	return weightedSum / totalWeight
}

// VAF computes the variance of the score series normalized by the mean.
func (s RunSeries) VAF() float64 {
	if len(s.Samples) == 0 {
		return 0
	}

	mean := s.mean()
	if mean == 0 {
		return 0
	}

	var sumSquares float64
	for _, sample := range s.Samples {
		delta := sample.Score - mean
		sumSquares += delta * delta
	}

	variance := sumSquares / float64(len(s.Samples))
	return variance / math.Abs(mean)
}

// MOP estimates the first sustained collapse point in the series.
//
// The result is normalized to [0,1] where 1 means no collapse was observed
// and lower values mean the collapse arrived earlier in the run series.
func (s RunSeries) MOP() float64 {
	if len(s.Samples) == 0 {
		return 0
	}

	onset := firstCollapseOnset(s.Samples, defaultMOPCollapseThreshold, defaultMOPCollapseWindow)
	if onset < 0 {
		return 1
	}
	if len(s.Samples) == 1 {
		return 0
	}
	return float64(onset) / float64(len(s.Samples)-1)
}

// GDS computes weighted partial credit across repeated runs for the named score.
func GDS(results []EvalResult, key string) (float64, error) {
	series, err := NewRunSeries(results, key)
	if err != nil {
		return 0, err
	}
	return series.GDS(), nil
}

// VAF computes normalized score variance across repeated runs for the named score.
func VAF(results []EvalResult, key string) (float64, error) {
	series, err := NewRunSeries(results, key)
	if err != nil {
		return 0, err
	}
	return series.VAF(), nil
}

// MOP computes the normalized collapse onset point across repeated runs.
func MOP(results []EvalResult, key string) (float64, error) {
	series, err := NewRunSeries(results, key)
	if err != nil {
		return 0, err
	}
	return series.MOP(), nil
}

func (s RunSeries) mean() float64 {
	if len(s.Samples) == 0 {
		return 0
	}

	var sum float64
	for _, sample := range s.Samples {
		sum += sample.Score
	}
	return sum / float64(len(s.Samples))
}

func firstCollapseOnset(samples []RunSample, threshold float64, window int) int {
	if len(samples) == 0 {
		return -1
	}
	if window <= 1 {
		for i, sample := range samples {
			if sample.Score <= threshold {
				return i
			}
		}
		return -1
	}

	if len(samples) < window {
		if allBelowThreshold(samples, threshold) {
			return 0
		}
		return -1
	}

	for i := 0; i <= len(samples)-window; i++ {
		ok := true
		for j := 0; j < window; j++ {
			if samples[i+j].Score > threshold {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func allBelowThreshold(samples []RunSample, threshold float64) bool {
	for _, sample := range samples {
		if sample.Score > threshold {
			return false
		}
	}
	return true
}
