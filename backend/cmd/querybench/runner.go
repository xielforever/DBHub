package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/dbhub/internal/queryapi"
)

type latencySummary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms,omitempty"`
	P95   float64 `json:"p95_ms,omitempty"`
	P99   float64 `json:"p99_ms,omitempty"`
	Max   float64 `json:"max_ms,omitempty"`
}

func summarize(values []float64) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	sort.Float64s(values)
	at := func(p float64) float64 { return values[int(math.Ceil(float64(len(values))*p))-1] }
	return latencySummary{len(values), at(.5), at(.95), at(.99), values[len(values)-1]}
}

type sample struct {
	elapsed       time.Duration
	status, bytes int
	valid         bool
}
type phasePeaks struct {
	Samples             int                            `json:"samples"`
	Running             int                            `json:"running"`
	Waiting             int                            `json:"waiting"`
	RSS                 uint64                         `json:"rss_bytes"`
	Heap                uint64                         `json:"go_heap_alloc_bytes"`
	Goroutines          int                            `json:"goroutines"`
	MetadataAcquired    int32                          `json:"metadata_acquired_connections"`
	DatabaseConnections map[string]databaseConnections `json:"database_connections"`
	SamplingErrors      int                            `json:"sampling_errors"`
}

func (p *phasePeaks) observe(s queryapi.QueryMetricsSnapshot, conns map[string]databaseConnections) {
	p.Samples++
	p.Running = max(p.Running, s.Queue.Running)
	p.Waiting = max(p.Waiting, s.Queue.Waiting)
	if s.Process.RSSBytes != nil {
		p.RSS = max(p.RSS, *s.Process.RSSBytes)
	}
	p.Heap = max(p.Heap, s.Process.HeapAllocBytes)
	p.Goroutines = max(p.Goroutines, s.Process.Goroutines)
	p.MetadataAcquired = max(p.MetadataAcquired, s.MetadataPool.AcquiredConnections)
	for kind, c := range conns {
		old := p.DatabaseConnections[kind]
		p.DatabaseConnections[kind] = databaseConnections{max(old.Total, c.Total), max(old.Active, c.Active)}
	}
}

type metricDelta struct {
	Requests              uint64            `json:"requests"`
	Executions            uint64            `json:"executions"`
	Outcomes              map[string]uint64 `json:"request_outcomes"`
	Timeouts              uint64            `json:"timeouts"`
	Canceled              uint64            `json:"canceled"`
	QueueOverload         uint64            `json:"queue_overload"`
	QueueTimeouts         uint64            `json:"queue_wait_timeouts"`
	QueueWaitMeanMS       float64           `json:"queue_wait_mean_ms"`
	QueueWaitP95UpperMS   *float64          `json:"queue_wait_p95_upper_ms,omitempty"`
	MetadataEmptyAcquires int64             `json:"metadata_empty_acquires"`
	MetadataWaitSeconds   float64           `json:"metadata_wait_seconds"`
}
type phaseReport struct {
	Database          string         `json:"database"`
	Workload          string         `json:"workload"`
	Concurrency       int            `json:"concurrency"`
	ElapsedSeconds    float64        `json:"elapsed_seconds"`
	Requests          int            `json:"requests"`
	HTTPStatuses      map[int]int    `json:"http_statuses"`
	SuccessRPS        float64        `json:"success_rps"`
	AttemptRPS        float64        `json:"attempt_rps"`
	ResponseBytes     int64          `json:"response_payload_bytes"`
	SuccessLatency    latencySummary `json:"success_latency"`
	RejectedLatency   latencySummary `json:"rejected_latency"`
	AllLatency        latencySummary `json:"all_latency"`
	AverageCPUPercent *float64       `json:"backend_average_cpu_percent,omitempty"`
	Peaks             phasePeaks     `json:"observed_peaks"`
	Metrics           metricDelta    `json:"metrics_delta"`
	Valid             bool           `json:"valid"`
}
type phaseTracker struct {
	f        *fixtures
	before   queryapi.QueryMetricsSnapshot
	started  time.Time
	stop     chan struct{}
	done     chan phasePeaks
	stopOnce sync.Once
	peaks    phasePeaks
}

func beginPhase(ctx context.Context, f *fixtures) (*phaseTracker, error) {
	before, err := f.c.idle(ctx)
	if err != nil {
		return nil, err
	}
	conns, err := f.connections(ctx)
	if err != nil {
		return nil, err
	}
	p := &phaseTracker{f: f, before: before, stop: make(chan struct{}), done: make(chan phasePeaks, 1), started: time.Now()}
	go func() {
		peaks := phasePeaks{DatabaseConnections: map[string]databaseConnections{}}
		peaks.observe(before, conns)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		defer func() { p.done <- peaks }()
		for {
			select {
			case <-p.stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s, err := f.c.metrics(ctx)
				if err != nil {
					peaks.SamplingErrors++
					continue
				}
				if !s.StartedAt.Equal(before.StartedAt) {
					peaks.SamplingErrors++
					continue
				}
				connections, err := f.connections(ctx)
				if err != nil {
					peaks.SamplingErrors++
					continue
				}
				peaks.observe(s, connections)
			}
		}
	}()
	return p, nil
}
func histogramP95(before, after queryapi.DurationDistribution) *float64 {
	if len(before.Buckets) != len(after.Buckets) {
		return nil
	}
	count := after.Count - before.Count
	if count == 0 {
		return nil
	}
	rank := uint64(math.Ceil(float64(count) * .95))
	for i, b := range after.Buckets {
		if b.Count-before.Buckets[i].Count >= rank {
			v := b.UpperBoundSeconds * 1000
			return &v
		}
	}
	return nil
}
func (p *phaseTracker) stopSampling() phasePeaks {
	p.stopOnce.Do(func() { close(p.stop); p.peaks = <-p.done })
	return p.peaks
}
func (p *phaseTracker) finish(ctx context.Context, t target, name string, concurrency int, samples []sample) (phaseReport, error) {
	r := phaseReport{Database: t.kind, Workload: name, Concurrency: concurrency, Requests: len(samples), HTTPStatuses: map[int]int{}}
	after, err := p.f.c.idle(ctx)
	r.Peaks = p.stopSampling()
	if err != nil {
		return r, err
	}
	conns, err := p.f.connections(ctx)
	if err != nil {
		return r, err
	}
	r.Peaks.observe(after, conns)
	r.ElapsedSeconds = time.Since(p.started).Seconds()
	if !after.StartedAt.Equal(p.before.StartedAt) {
		return r, fmt.Errorf("后端实例已改变，当前基线无效")
	}
	var all, success, rejected []float64
	valid := true
	for _, s := range samples {
		r.HTTPStatuses[s.status]++
		r.ResponseBytes += int64(s.bytes)
		all = append(all, float64(s.elapsed)/float64(time.Millisecond))
		if s.status == 200 {
			success = append(success, float64(s.elapsed)/float64(time.Millisecond))
		} else if s.status == 429 {
			rejected = append(rejected, float64(s.elapsed)/float64(time.Millisecond))
		}
		valid = valid && s.valid
	}
	r.SuccessLatency = summarize(success)
	r.RejectedLatency = summarize(rejected)
	r.AllLatency = summarize(all)
	r.SuccessRPS = float64(len(success)) / r.ElapsedSeconds
	r.AttemptRPS = float64(len(samples)) / r.ElapsedSeconds
	b := p.before
	r.Metrics = metricDelta{Requests: after.Requests.Total - b.Requests.Total, Executions: after.Executions.Total - b.Executions.Total, Outcomes: map[string]uint64{}, Timeouts: after.Counters.Timeouts - b.Counters.Timeouts, Canceled: after.Counters.Canceled - b.Counters.Canceled, QueueOverload: after.Queue.Overload - b.Queue.Overload, QueueTimeouts: after.Queue.Timeouts - b.Queue.Timeouts, QueueWaitP95UpperMS: histogramP95(b.Queue.WaitDuration, after.Queue.WaitDuration), MetadataEmptyAcquires: after.MetadataPool.EmptyAcquireCount - b.MetadataPool.EmptyAcquireCount, MetadataWaitSeconds: after.MetadataPool.EmptyAcquireWaitSeconds - b.MetadataPool.EmptyAcquireWaitSeconds}
	for outcome, v := range after.Requests.Completed {
		r.Metrics.Outcomes[outcome] = v.Count - b.Requests.Completed[outcome].Count
	}
	if n := after.Queue.WaitDuration.Count - b.Queue.WaitDuration.Count; n > 0 {
		r.Metrics.QueueWaitMeanMS = (after.Queue.WaitDuration.SumSeconds - b.Queue.WaitDuration.SumSeconds) * 1000 / float64(n)
	}
	if b.Process.CPUSeconds != nil && after.Process.CPUSeconds != nil {
		v := (*after.Process.CPUSeconds - *b.Process.CPUSeconds) / after.CapturedAt.Sub(b.CapturedAt).Seconds() * 100
		r.AverageCPUPercent = &v
	}
	valid = valid && r.Metrics.Requests == uint64(len(samples)) && r.Peaks.SamplingErrors == 0 && after.Queue.PeakRunning <= after.Queue.Limits.GlobalConcurrent
	var completed uint64
	for _, n := range r.Metrics.Outcomes {
		completed += n
	}
	valid = valid && completed == r.Metrics.Requests
	r.Valid = valid
	if !valid {
		return r, fmt.Errorf("压测响应、采样或指标对账失败：%s/%s/c%d", t.kind, name, concurrency)
	}
	return r, nil
}
func readSample(c *apiClient, ctx context.Context, a actor, t target, workload string, sequence, rows int) sample {
	params := map[string]any{}
	query := ""
	first, expectedRows := 0, 1
	switch workload {
	case "point":
		first = sequence%rows + 1
		params["id"] = first
		query = "SELECT id,amount,label FROM " + t.table + " WHERE id=:id"
	case "range100":
		first = sequence%(rows-99) + 1
		expectedRows = 100
		params["start"] = first
		query = "SELECT id,amount,label FROM " + t.table + " WHERE id>=:start ORDER BY id LIMIT 100"
	case "overload":
		if t.kind == "postgres" {
			query = "SELECT pg_sleep(0.3) AS pause_complete"
		} else {
			query = "SELECT SLEEP(0.3) AS pause_complete"
		}
	}
	r := c.call(ctx, "POST", "/api/v1/query/execute", a.token, map[string]any{"request_id": newID(), "connection_id": t.id, "database": t.database, "sql": query, "params": params, "timeout_ms": 5000, "max_rows": 1000})
	s := sample{elapsed: r.elapsed, status: r.status, bytes: r.bytes}
	if workload == "overload" && r.status == 429 && r.err == nil {
		var receipt struct {
			Status string `json:"status"`
		}
		s.valid = json.Unmarshal(r.data, &receipt) == nil && receipt.Status == "rejected"
		return s
	}
	var result struct {
		Kind      string              `json:"kind"`
		Rows      [][]json.RawMessage `json:"rows"`
		Truncated bool                `json:"truncated"`
	}
	if r.err != nil || r.status != 200 || r.code != 0 || json.Unmarshal(r.data, &result) != nil || result.Kind != "query" || len(result.Rows) != expectedRows || result.Truncated {
		return s
	}
	s.valid = true
	if workload != "overload" {
		for i, row := range result.Rows {
			if len(row) != 3 {
				s.valid = false
				break
			}
			id, err := numeric(row[0])
			amount, aErr := numeric(row[1])
			var label string
			lErr := json.Unmarshal(row[2], &label)
			want := first + i
			if err != nil || aErr != nil || lErr != nil || id != want || amount != (want*3)%1000 || label != fmt.Sprintf("row-%06d", want) {
				s.valid = false
				break
			}
		}
	}
	return s
}
func runReadPhase(ctx context.Context, f *fixtures, t target, workload string, concurrency, rows int, duration time.Duration) (phaseReport, error) {
	p, err := beginPhase(ctx, f)
	if err != nil {
		return phaseReport{}, err
	}
	defer p.stopSampling()
	results := make(chan sample, concurrency*2)
	start := make(chan struct{})
	deadline := time.Now().Add(duration)
	var sequence atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for {
				if ctx.Err() != nil {
					return
				}
				if workload != "overload" && time.Now().After(deadline) {
					return
				}
				results <- readSample(f.c, ctx, f.actors[worker%len(f.actors)], t, workload, int(sequence.Add(1)), rows)
				if workload == "overload" {
					return
				}
			}
		}(i)
	}
	close(start)
	go func() { wg.Wait(); close(results) }()
	var samples []sample
	for s := range results {
		samples = append(samples, s)
	}
	r, err := p.finish(ctx, t, workload, concurrency, samples)
	if err == nil && (r.Metrics.Executions != uint64(r.SuccessLatency.Count) || r.Metrics.Outcomes["succeeded"] != uint64(r.SuccessLatency.Count) || r.Metrics.Outcomes["rejected"] != uint64(r.RejectedLatency.Count)) {
		r.Valid = false
		return r, fmt.Errorf("读取响应与执行次数不一致")
	}
	if err == nil && workload == "overload" && r.HTTPStatuses[429] == 0 {
		r.Valid = false
		return r, fmt.Errorf("过载场景没有触发限流，请加大受控并发后重新测量")
	}
	return r, err
}
