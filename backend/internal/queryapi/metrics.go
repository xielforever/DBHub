package queryapi

import (
	"context"
	"math"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// 固定桶和结果枚举限制指标基数，不使用 SQL、用户、连接或请求编号作为标签。
var durationBounds = [...]float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300}
var requestOutcomes = [...]string{"succeeded", "failed", "rejected", "unknown", "duplicate", "conflict", "unavailable", "internal_error"}
var executionOutcomes = [...]string{"succeeded", "failed", "unknown"}

type DurationBucket struct {
	UpperBoundSeconds float64 `json:"le_seconds"`
	Count             uint64  `json:"count"`
}
type DurationDistribution struct {
	Count      uint64           `json:"count"`
	SumSeconds float64          `json:"sum_seconds"`
	MaxSeconds float64          `json:"max_seconds"`
	Buckets    []DurationBucket `json:"buckets"`
}
type durationHistogram struct {
	count    uint64
	sum, max float64
	buckets  [len(durationBounds)]uint64
}

func (h *durationHistogram) observe(elapsed time.Duration) {
	seconds := math.Max(0, elapsed.Seconds())
	h.count++
	h.sum += seconds
	h.max = math.Max(h.max, seconds)
	for i, upper := range durationBounds {
		if seconds <= upper {
			h.buckets[i]++
		}
	}
}
func (h durationHistogram) snapshot() DurationDistribution {
	d := DurationDistribution{Count: h.count, SumSeconds: h.sum, MaxSeconds: h.max, Buckets: make([]DurationBucket, len(durationBounds))}
	for i, upper := range durationBounds {
		d.Buckets[i] = DurationBucket{upper, h.buckets[i]}
	}
	return d
}

type OutcomeMetrics struct {
	Total     uint64                          `json:"total"`
	InFlight  uint64                          `json:"in_flight"`
	Completed map[string]DurationDistribution `json:"completed"`
}
type QueryMetricCounters struct {
	Timeouts           uint64 `json:"timeouts_total"`
	Canceled           uint64 `json:"canceled_total"`
	RegistrationErrors uint64 `json:"registration_errors_total"`
	ReceiptWriteErrors uint64 `json:"receipt_write_errors_total"`
}
type ProcessMetrics struct {
	GoVersion      string   `json:"go_version"`
	GOMAXPROCS     int      `json:"gomaxprocs"`
	LogicalCPUs    int      `json:"logical_cpus"`
	Goroutines     int      `json:"goroutines"`
	HeapAllocBytes uint64   `json:"go_heap_alloc_bytes"`
	HeapSysBytes   uint64   `json:"go_heap_sys_bytes"`
	CPUSeconds     *float64 `json:"cpu_seconds_total,omitempty"`
	RSSBytes       *uint64  `json:"rss_bytes,omitempty"`
}
type QueryMetricsSnapshot struct {
	StartedAt             time.Time                  `json:"started_at"`
	CapturedAt            time.Time                  `json:"captured_at"`
	UptimeSeconds         float64                    `json:"uptime_seconds"`
	Requests              OutcomeMetrics             `json:"requests"`
	Executions            OutcomeMetrics             `json:"executions"`
	Counters              QueryMetricCounters        `json:"counters"`
	Queue                 QueueMetrics               `json:"queue"`
	Process               ProcessMetrics             `json:"process"`
	ReceiptStoreAvailable bool                       `json:"receipt_store_available"`
	Receipts              *db.ExecutionReceiptCounts `json:"receipts_last_24h,omitempty"`
	MetadataPool          db.MetadataPoolMetrics     `json:"metadata_pool"`
}
type queryMetrics struct {
	mu                                                     sync.Mutex
	started                                                time.Time
	requests, requestsActive, executions, executionsActive uint64
	requestDuration                                        map[string]*durationHistogram
	executionDuration                                      map[string]*durationHistogram
	counters                                               QueryMetricCounters
}

func newQueryMetrics() *queryMetrics {
	m := &queryMetrics{started: time.Now(), requestDuration: map[string]*durationHistogram{}, executionDuration: map[string]*durationHistogram{}}
	for _, outcome := range requestOutcomes {
		m.requestDuration[outcome] = &durationHistogram{}
	}
	for _, outcome := range executionOutcomes {
		m.executionDuration[outcome] = &durationHistogram{}
	}
	return m
}
func (m *queryMetrics) beginRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	m.requestsActive++
}
func (m *queryMetrics) finishRequest(outcome string, elapsed time.Duration, timedOut, canceled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsActive--
	if m.requestDuration[outcome] == nil {
		outcome = "internal_error"
	}
	m.requestDuration[outcome].observe(elapsed)
	if timedOut {
		m.counters.Timeouts++
	}
	if canceled {
		m.counters.Canceled++
	}
}
func (m *queryMetrics) beginExecution() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executions++
	m.executionsActive++
}
func (m *queryMetrics) finishExecution(outcome string, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executionsActive--
	if m.executionDuration[outcome] == nil {
		outcome = "unknown"
	}
	m.executionDuration[outcome].observe(elapsed)
}
func (m *queryMetrics) registrationError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters.RegistrationErrors++
}
func (m *queryMetrics) receiptWriteError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters.ReceiptWriteErrors++
}
func (m *queryMetrics) snapshot() QueryMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := QueryMetricsSnapshot{StartedAt: m.started.UTC(), CapturedAt: time.Now().UTC(), UptimeSeconds: time.Since(m.started).Seconds(), Counters: m.counters}
	s.Requests = OutcomeMetrics{m.requests, m.requestsActive, make(map[string]DurationDistribution, len(requestOutcomes))}
	s.Executions = OutcomeMetrics{m.executions, m.executionsActive, make(map[string]DurationDistribution, len(executionOutcomes))}
	for k, v := range m.requestDuration {
		s.Requests.Completed[k] = v.snapshot()
	}
	for k, v := range m.executionDuration {
		s.Executions.Completed[k] = v.snapshot()
	}
	return s
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricsResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func requestOutcome(status int) string {
	if status == 0 {
		return "internal_error"
	}
	if status >= 500 {
		return "unavailable"
	}
	return "rejected"
}

// QueryMetrics 提供管理员运行指标。元数据库故障时保留本进程指标并明确标记不可用。
func (h *Handler) QueryMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "prometheus" {
		httpx.Fail(w, httpx.BadRequest("指标格式仅支持 json 或 prometheus"))
		return
	}
	s := h.metrics.snapshot()
	s.Queue = h.jobs.metricsSnapshot()
	s.MetadataPool = h.controls.MetadataPoolMetrics()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.Process = ProcessMetrics{GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0), LogicalCPUs: runtime.NumCPU(), Goroutines: runtime.NumGoroutine(), HeapAllocBytes: mem.HeapAlloc, HeapSysBytes: mem.HeapSys}
	s.Process.CPUSeconds, s.Process.RSSBytes = processResources()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	counts, err := h.controls.ExecutionReceiptCounts(ctx)
	if err == nil {
		s.ReceiptStoreAvailable = true
		s.Receipts = &counts
	}
	if format == "prometheus" {
		writePrometheus(w, s)
		return
	}
	httpx.OK(w, s)
}
