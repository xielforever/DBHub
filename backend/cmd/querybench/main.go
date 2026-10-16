// querybench 为本地 Cloud 开发环境建立单实例 SQL 工作台容量基线。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/user/dbhub/internal/queryapi"
)

type benchmarkReport struct {
	SchemaVersion           int                       `json:"schema_version"`
	StartedAt               time.Time                 `json:"started_at"`
	FinishedAt              time.Time                 `json:"finished_at"`
	SourceRevision          string                    `json:"source_revision"`
	SourceDirty             bool                      `json:"source_dirty"`
	Environment             string                    `json:"environment"`
	BackendVersion          string                    `json:"backend_version"`
	BackendProcess          queryapi.ProcessMetrics   `json:"backend_process_before"`
	Limits                  queryapi.QueryQueueLimits `json:"limits"`
	MetadataMaxConnections  int32                     `json:"metadata_max_connections"`
	RowsPerTable            int                       `json:"rows_per_table"`
	Users                   int                       `json:"users"`
	DurationPerPhaseSeconds float64                   `json:"duration_per_phase_seconds"`
	SamplingIntervalSeconds float64                   `json:"sampling_interval_seconds"`
	LoadModel               string                    `json:"load_model"`
	RunnerGoVersion         string                    `json:"runner_go_version"`
	TargetVersions          map[string]string         `json:"target_versions"`
	Phases                  []phaseReport             `json:"phases"`
	CleanupSucceeded        bool                      `json:"cleanup_succeeded"`
	CleanupNamespace        string                    `json:"cleanup_namespace,omitempty"`
	Valid                   bool                      `json:"valid"`
	Failure                 string                    `json:"failure,omitempty"`
}

func run(ctx context.Context, c *apiClient, report *benchmarkReport, rows int, duration time.Duration, queueTimeout bool) (err error) {
	var health struct {
		Env     string `json:"env"`
		Version string `json:"version"`
	}
	healthResponse := c.call(ctx, "GET", "/api/health", "", nil)
	if healthResponse.err != nil || healthResponse.status != 200 || json.Unmarshal(healthResponse.data, &health) != nil {
		return fmt.Errorf("后端健康检查失败")
	}
	if health.Env != "development" && health.Env != "test" {
		return fmt.Errorf("压测仅支持 development/test 环境")
	}
	report.Environment = health.Env
	report.BackendVersion = health.Version
	for _, name := range []string{"APP_ADMIN_USERNAME", "APP_ADMIN_PASSWORD", "TEST_POSTGRES_DSN", "TEST_MYSQL_DSN"} {
		if os.Getenv(name) == "" {
			return fmt.Errorf("缺少环境变量 %s", name)
		}
	}
	c.admin, err = c.login(ctx, os.Getenv("APP_ADMIN_USERNAME"), os.Getenv("APP_ADMIN_PASSWORD"))
	if err != nil {
		return err
	}
	initial, err := c.idle(ctx)
	if err != nil {
		return err
	}
	report.BackendProcess = initial.Process
	report.Limits = initial.Queue.Limits
	report.MetadataMaxConnections = initial.MetadataPool.MaxConnections
	f := &fixtures{c: c}
	defer func() {
		if cleanupErr := f.cleanup(); cleanupErr != nil {
			report.CleanupNamespace = f.marker
			err = cleanupErr
		} else {
			report.CleanupSucceeded = true
		}
		c.http.CloseIdleConnections()
	}()
	if err = f.prepare(ctx, rows); err != nil {
		return err
	}
	report.Users = len(f.actors)
	report.TargetVersions = map[string]string{}
	for _, t := range f.targets {
		report.TargetVersions[t.kind] = t.version
	}
	appendPhase := func(r phaseReport, phaseErr error) error {
		report.Phases = append(report.Phases, r)
		fmt.Printf("%s %-25s c=%-2d requests=%-5d success_rps=%7.1f success_p95_ms=%7.1f statuses=%v valid=%t\n", r.Database, r.Workload, r.Concurrency, r.Requests, r.SuccessRPS, r.SuccessLatency.P95, r.HTTPStatuses, r.Valid)
		return phaseErr
	}
	for _, t := range f.targets {
		for _, workload := range []string{"point", "range100"} {
			// 预热不纳入阶段测量；执行路径和结果仍使用真实 API 校验。
			for i := 0; i < 8; i++ {
				if !readSample(c, ctx, f.actors[i%len(f.actors)], t, workload, i, rows).valid {
					return fmt.Errorf("预热查询未通过结果校验")
				}
			}
			for _, concurrency := range []int{1, 4, 8, 16, 32} {
				r, phaseErr := runReadPhase(ctx, f, t, workload, concurrency, rows, duration)
				if err = appendPhase(r, phaseErr); err != nil {
					return err
				}
			}
		}
		r, phaseErr := runReadPhase(ctx, f, t, "overload", 64, rows, 0)
		if err = appendPhase(r, phaseErr); err != nil {
			return err
		}
		r, phaseErr = runTimeoutProbe(ctx, f, t)
		if err = appendPhase(r, phaseErr); err != nil {
			return err
		}
		r, phaseErr = runCancelProbe(ctx, f, t)
		if err = appendPhase(r, phaseErr); err != nil {
			return err
		}
	}
	if queueTimeout {
		r, phaseErr := runQueueTimeoutProbe(ctx, f, f.targets[0])
		if err = appendPhase(r, phaseErr); err != nil {
			return err
		}
	}
	return nil
}
func main() {
	base := flag.String("base-url", "http://backend:8080", "开发后端地址")
	duration := flag.Duration("duration", 5*time.Second, "每个稳定负载阶段的持续时间，2s–30s")
	rows := flag.Int("rows", 10000, "每个独立测试表的数据量，1000–100000")
	queueTimeout := flag.Bool("queue-timeout", true, "验证真实的 30 秒排队超时")
	output := flag.String("output", "/runtime/benchmark-artifacts/query-baseline-"+time.Now().UTC().Format("20060102T150405Z")+".json", "脱敏报告文件，不覆盖已有文件")
	flag.Parse()
	u, err := url.Parse(*base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || *duration < 2*time.Second || *duration > 30*time.Second || *rows < 1000 || *rows > 100000 {
		fmt.Fprintln(os.Stderr, "压测参数无效")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	report := benchmarkReport{SchemaVersion: 1, StartedAt: time.Now().UTC(), SourceRevision: os.Getenv("DBHUB_BENCH_REVISION"), SourceDirty: os.Getenv("DBHUB_BENCH_DIRTY") == "true", RowsPerTable: *rows, DurationPerPhaseSeconds: duration.Seconds(), SamplingIntervalSeconds: .5, LoadModel: "closed-loop, zero think time; four isolated developer users; separate runner container", RunnerGoVersion: runtime.Version()}
	err = run(ctx, newClient(*base), &report, *rows, *duration, *queueTimeout)
	report.FinishedAt = time.Now().UTC()
	report.Valid = err == nil && report.CleanupSucceeded
	if err != nil {
		report.Failure = err.Error()
	}
	if writeErr := writeReport(*output, report); writeErr != nil {
		fmt.Fprintln(os.Stderr, writeErr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("基线完成：%d 个阶段，测试对象已清理；报告 %s\n", len(report.Phases), *output)
}
func writeReport(path string, report benchmarkReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("无法创建报告目录")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("无法创建报告文件（不会覆盖已有文件）")
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		return fmt.Errorf("报告写入失败")
	}
	return nil
}
