package main

import (
	"testing"

	"github.com/user/dbhub/internal/queryapi"
)

func TestLatencySummaryUsesNearestRankAndHandlesNoSuccesses(t *testing.T) {
	var values []float64
	for i := 100; i >= 1; i-- {
		values = append(values, float64(i))
	}
	s := summarize(values)
	if s.Count != 100 || s.P50 != 50 || s.P95 != 95 || s.P99 != 99 || s.Max != 100 {
		t.Fatalf("incorrect percentile calculation: %+v", s)
	}
	if empty := summarize(nil); empty.Count != 0 {
		t.Fatal("missing successes were reported as measured zero latency")
	}
}
func TestHistogramP95UsesPhaseDeltaInsteadOfLifetimeDistribution(t *testing.T) {
	before := queryapi.DurationDistribution{Count: 100, Buckets: []queryapi.DurationBucket{{UpperBoundSeconds: .1, Count: 100}, {UpperBoundSeconds: 1, Count: 100}, {UpperBoundSeconds: 10, Count: 100}}}
	after := queryapi.DurationDistribution{Count: 102, Buckets: []queryapi.DurationBucket{{UpperBoundSeconds: .1, Count: 100}, {UpperBoundSeconds: 1, Count: 101}, {UpperBoundSeconds: 10, Count: 102}}}
	p95 := histogramP95(before, after)
	if p95 == nil || *p95 != 10000 {
		t.Fatalf("old fast samples contaminated the new phase percentile: %v", p95)
	}
	if histogramP95(after, after) != nil {
		t.Fatal("empty phase had a measured percentile")
	}
}
