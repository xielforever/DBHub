package queryapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

func metric(w io.Writer, name, kind, help string, value any) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %v\n", name, help, name, kind, name, value)
}
func histogram(w io.Writer, name, label string, d DurationDistribution) {
	labels := ""
	if label != "" {
		labels = `outcome="` + label + `",`
	}
	for _, bucket := range d.Buckets {
		fmt.Fprintf(w, "%s_bucket{%sle=\"%s\"} %d\n", name, labels, strconv.FormatFloat(bucket.UpperBoundSeconds, 'f', -1, 64), bucket.Count)
	}
	fmt.Fprintf(w, "%s_bucket{%sle=\"+Inf\"} %d\n", name, labels, d.Count)
	if label != "" {
		labels = `{outcome="` + label + `"}`
	}
	fmt.Fprintf(w, "%s_count%s %d\n%s_sum%s %g\n", name, labels, d.Count, name, labels, d.SumSeconds)
}
func writePrometheus(w http.ResponseWriter, s QueryMetricsSnapshot) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	metric(w, "dbhub_query_process_start_time_seconds", "gauge", "Start time of this query handler process.", float64(s.StartedAt.Unix()))
	metric(w, "dbhub_query_requests_total", "counter", "Authenticated execute requests, including duplicates and rejections.", s.Requests.Total)
	metric(w, "dbhub_query_requests_in_flight", "gauge", "Execute requests currently handled by this process.", s.Requests.InFlight)
	fmt.Fprintln(w, "# HELP dbhub_query_request_duration_seconds Server handling time, including registration, queue, execution and cleanup.\n# TYPE dbhub_query_request_duration_seconds histogram")
	for _, outcome := range requestOutcomes {
		histogram(w, "dbhub_query_request_duration_seconds", outcome, s.Requests.Completed[outcome])
	}
	metric(w, "dbhub_query_executions_total", "counter", "User SQL execution attempts started in this process.", s.Executions.Total)
	metric(w, "dbhub_query_executions_in_flight", "gauge", "User SQL executions currently running.", s.Executions.InFlight)
	fmt.Fprintln(w, "# HELP dbhub_query_execution_duration_seconds Target execution phase, excluding registration and queue.\n# TYPE dbhub_query_execution_duration_seconds histogram")
	for _, outcome := range executionOutcomes {
		histogram(w, "dbhub_query_execution_duration_seconds", outcome, s.Executions.Completed[outcome])
	}
	metric(w, "dbhub_query_timeouts_total", "counter", "Request timeouts, including queue wait deadlines.", s.Counters.Timeouts)
	metric(w, "dbhub_query_canceled_total", "counter", "Requests whose operation returned cancellation.", s.Counters.Canceled)
	metric(w, "dbhub_query_registration_errors_total", "counter", "Execution registration errors.", s.Counters.RegistrationErrors)
	metric(w, "dbhub_query_receipt_write_errors_total", "counter", "Execution receipt persistence errors.", s.Counters.ReceiptWriteErrors)
	metric(w, "dbhub_query_queue_running", "gauge", "Admitted requests holding a global execution slot.", s.Queue.Running)
	metric(w, "dbhub_query_queue_waiting", "gauge", "Registered requests waiting for execution slots.", s.Queue.Waiting)
	metric(w, "dbhub_query_queue_running_peak", "gauge", "Peak global slots held since process start.", s.Queue.PeakRunning)
	metric(w, "dbhub_query_queue_waiting_peak", "gauge", "Peak waiting requests since process start.", s.Queue.PeakWaiting)
	metric(w, "dbhub_query_queue_admitted_total", "counter", "Requests admitted into the bounded queue.", s.Queue.Admitted)
	metric(w, "dbhub_query_queue_overload_total", "counter", "Requests rejected because the queue is full.", s.Queue.Overload)
	metric(w, "dbhub_query_queue_shutdown_total", "counter", "Requests rejected after shutdown.", s.Queue.Shutdown)
	metric(w, "dbhub_query_queue_timeouts_total", "counter", "Requests whose queue wait deadline expired.", s.Queue.Timeouts)
	metric(w, "dbhub_query_queue_canceled_total", "counter", "Requests canceled while waiting.", s.Queue.Canceled)
	fmt.Fprintln(w, "# HELP dbhub_query_queue_wait_duration_seconds Wait before admission or abandonment; overload rejections excluded.\n# TYPE dbhub_query_queue_wait_duration_seconds histogram")
	histogram(w, "dbhub_query_queue_wait_duration_seconds", "", s.Queue.WaitDuration)
	metric(w, "dbhub_query_go_heap_alloc_bytes", "gauge", "Currently allocated Go heap bytes.", s.Process.HeapAllocBytes)
	metric(w, "dbhub_query_go_goroutines", "gauge", "Current Go goroutine count.", s.Process.Goroutines)
	metric(w, "dbhub_query_metadata_connections", "gauge", "Current metadata pool connections.", s.MetadataPool.TotalConnections)
	metric(w, "dbhub_query_metadata_connections_acquired", "gauge", "Current acquired metadata connections.", s.MetadataPool.AcquiredConnections)
	metric(w, "dbhub_query_metadata_connections_max", "gauge", "Metadata pool connection limit.", s.MetadataPool.MaxConnections)
	metric(w, "dbhub_query_metadata_acquire_total", "counter", "Successful metadata pool acquires.", s.MetadataPool.AcquireCount)
	metric(w, "dbhub_query_metadata_empty_acquire_total", "counter", "Acquires that encountered an empty metadata pool.", s.MetadataPool.EmptyAcquireCount)
	metric(w, "dbhub_query_metadata_wait_seconds_total", "counter", "Time waiting on an empty metadata pool.", s.MetadataPool.EmptyAcquireWaitSeconds)
	if s.Process.CPUSeconds != nil {
		metric(w, "dbhub_query_process_cpu_seconds_total", "counter", "Process CPU seconds across all cores.", *s.Process.CPUSeconds)
	}
	if s.Process.RSSBytes != nil {
		metric(w, "dbhub_query_process_rss_bytes", "gauge", "Process resident set size in bytes.", *s.Process.RSSBytes)
	}
	available := 0
	if s.ReceiptStoreAvailable {
		available = 1
	}
	metric(w, "dbhub_query_receipt_store_available", "gauge", "Whether durable receipt counts could be read.", available)
	if s.Receipts != nil {
		fmt.Fprintln(w, "# HELP dbhub_query_receipts_last_24h Durable unfinished or unknown records created in the last 24 hours.\n# TYPE dbhub_query_receipts_last_24h gauge")
		fmt.Fprintf(w, "dbhub_query_receipts_last_24h{state=\"queued\"} %d\ndbhub_query_receipts_last_24h{state=\"running\"} %d\ndbhub_query_receipts_last_24h{state=\"unknown\"} %d\n", s.Receipts.Queued, s.Receipts.Running, s.Receipts.Unknown)
	}
}
