package queryapi

type QueryQueueLimits struct {
	GlobalConcurrent  int     `json:"global_concurrent"`
	PerUserConcurrent int     `json:"per_user_concurrent"`
	MaxTasks          int     `json:"max_tasks"`
	MaxUserTasks      int     `json:"max_user_tasks"`
	WaitSeconds       float64 `json:"wait_seconds"`
}
type QueueMetrics struct {
	Running      int                  `json:"running"`
	Waiting      int                  `json:"waiting"`
	PeakRunning  int                  `json:"peak_running"`
	PeakWaiting  int                  `json:"peak_waiting"`
	Admitted     uint64               `json:"admitted_total"`
	Overload     uint64               `json:"overload_total"`
	Shutdown     uint64               `json:"shutdown_total"`
	Timeouts     uint64               `json:"wait_timeouts_total"`
	Canceled     uint64               `json:"wait_canceled_total"`
	WaitDuration DurationDistribution `json:"wait_duration"`
	Limits       QueryQueueLimits     `json:"limits"`
}
type queueTelemetry struct {
	running, waiting, peakRunning, peakWaiting       int
	admitted, overload, shutdown, timeouts, canceled uint64
	wait                                             durationHistogram
}

func (m *jobs) metricsSnapshot() QueueMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.telemetry
	return QueueMetrics{
		Running: q.running, Waiting: q.waiting, PeakRunning: q.peakRunning, PeakWaiting: q.peakWaiting,
		Admitted: q.admitted, Overload: q.overload, Shutdown: q.shutdown, Timeouts: q.timeouts, Canceled: q.canceled,
		WaitDuration: q.wait.snapshot(), Limits: QueryQueueLimits{m.limits.global, m.limits.user, m.limits.pending, m.limits.userPending, m.limits.wait.Seconds()},
	}
}
