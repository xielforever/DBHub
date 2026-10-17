package queryapi

import (
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDurationDistributionHasCumulativeBucketsAndOverflow(t *testing.T) {
	var h durationHistogram
	samples := []time.Duration{time.Millisecond, 7 * time.Millisecond, 200 * time.Millisecond, 3 * time.Second, 400 * time.Second}
	for _, sample := range samples {
		h.observe(sample)
	}
	d := h.snapshot()
	if d.Count != 5 || math.Abs(d.SumSeconds-403.208) > 1e-9 || d.MaxSeconds != 400 {
		t.Fatalf("invalid distribution: %+v", d)
	}
	for _, bucket := range d.Buckets {
		var want uint64
		for _, sample := range samples {
			if sample.Seconds() <= bucket.UpperBoundSeconds {
				want++
			}
		}
		if bucket.Count != want {
			t.Fatalf("bucket %g: count=%d, want=%d", bucket.UpperBoundSeconds, bucket.Count, want)
		}
	}
	// 快照可以安全交给编码器，后续观察和外部修改不改变彼此的数据。
	d.Buckets[0].Count = 999
	h.observe(time.Second)
	if h.snapshot().Buckets[0].Count != 1 || d.Count != 5 {
		t.Fatal("snapshot shared mutable buckets")
	}
}

func TestQueryMetricsConcurrentAccounting(t *testing.T) {
	m := newQueryMetrics()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m.beginRequest()
				m.beginExecution()
				m.finishExecution("succeeded", time.Millisecond)
				m.finishRequest("succeeded", 2*time.Millisecond, false, false)
				_ = m.snapshot()
			}
		}()
	}
	wg.Wait()
	s := m.snapshot()
	if s.Requests.Total != 2500 || s.Requests.InFlight != 0 || s.Executions.InFlight != 0 || s.Executions.Completed["succeeded"].Count != 2500 {
		t.Fatalf("accounting mismatch: %+v", s)
	}
	m.beginRequest()
	m.finishRequest("unexpected-dynamic-label", time.Second, true, true)
	s = m.snapshot()
	if len(s.Requests.Completed) != len(requestOutcomes) || s.Requests.Completed["internal_error"].Count != 1 || s.Counters.Timeouts != 1 || s.Counters.Canceled != 1 {
		t.Fatal("unbounded labels or lost counters")
	}
}

func TestPrometheusContainsHistogramOverflowAndFixedLabels(t *testing.T) {
	m := newQueryMetrics()
	m.beginRequest()
	m.finishRequest("succeeded", 400*time.Second, false, false)
	s := m.snapshot()
	s.Queue = newJobs().metricsSnapshot()
	w := httptest.NewRecorder()
	writePrometheus(w, s)
	for _, expected := range []string{
		`dbhub_query_request_duration_seconds_bucket{outcome="succeeded",le="+Inf"} 1`,
		`dbhub_query_request_duration_seconds_count{outcome="succeeded"} 1`,
		`dbhub_query_request_duration_seconds_sum{outcome="succeeded"} 400`,
		`dbhub_query_receipt_store_available 0`,
	} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("missing metric: %s", expected)
		}
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatal("incorrect exporter content type")
	}
	if strings.Contains(w.Body.String(), `state="unknown"`) {
		t.Fatal("unavailable durable state was exposed as a known zero")
	}
}
