# SQL 工作台运行指标与压测

本轮增加后端运行观测和可重复的单实例容量基线，不改变第一阶段工作台界面，也不引入热点表或查询结果缓存。

首份完整测量见 [2026-10-16 容量基线](../benchmarks/SQL工作台容量基线-2026-10-16.md)，包含固定源码版本、27 阶段原始数据和当前并发配置建议。

## 指标接口

`GET /api/v1/metrics/query` 返回 JSON 信封；增加 `?format=prometheus` 返回 Prometheus 0.0.4 文本。两种格式均要求有效管理员 Bearer 身份，其他角色返回 403，未登录返回 401。响应禁止缓存；接口不会连接业务数据源，也不包含 SQL、参数、用户、连接或请求编号标签。

| 指标 | 口径 |
| --- | --- |
| `requests` | 已通过认证并进入执行处理器的请求，包括重复、参数拒绝和过载；`total = in_flight + completed 各结果之和` |
| `executions` | 成功持久化开始状态后，实际进入用户 SQL 执行阶段的次数；重复请求、预检失败和队列拒绝不计入 |
| 请求结果 | succeeded / failed / rejected / unknown / duplicate / conflict / unavailable / internal_error，互斥分类 |
| SQL 结果 | succeeded / failed / unknown；目标数据库已成功、但回执保存失败时，SQL 成功与请求 unknown 可以同时出现 |
| 超时、取消 | 操作返回的真实原因；排队超时也计入超时。它们可能同时属于 rejected、failed 或 unknown，不能与结果分类再次相加 |
| 请求耗时 | 服务端处理总时长，包含登记、排队、连接、执行和清理 |
| SQL 耗时 | 从开始发送用户 SQL 到执行引擎返回，包含该引擎的清理，不含登记、排队和打开连接 |
| 排队耗时 | 已进入有界队列的请求，从登记到取得槽位或放弃的时间；立即过载拒绝不计入 |
| `queue` | 当前运行/等待数量、进程内峰值、接纳/过载/关闭拒绝、等待超时/取消及当前限额 |
| `process` | Go 版本、GOMAXPROCS、可见逻辑 CPU、goroutine、Go 堆；Linux 额外提供累计进程 CPU 秒与当前 RSS |
| `metadata_pool` | 元数据库连接池当前/最大/占用连接，以及累计获取、等待和取消次数；无需额外查询数据库 |
| `receipts_last_24h` | 元数据库中近 24 小时创建的 queued/running/unknown 回执聚合；超过 5 分钟未完成的 queued/running 计入 unknown，stale 是其中子集 |

耗时使用固定累积直方图，单位为秒，桶上界从 1ms 到 300s，超出部分进入 `+Inf`。Prometheus 的 `_count` 提供按结果完成次数，`_sum` 为累计耗时；不把服务端直方图近似值当作精确 P95。

进程计数和峰值在重启后归零，使用 `started_at` 区分实例。持久化回执汇总跨重启存在，但仅覆盖上述 24 小时创建窗口。用户在界面点击“已核实，继续”不改写服务器历史回执，因此 unknown 记录数不等于当前待办数。

回执汇总查询使用未完成状态的部分索引。数据库不可读时仍返回本进程观测，明确给出 `receipt_store_available=false`，省略持久化数量；Prometheus 输出对应可用性 0，不伪装成零条未知记录。

主要 Prometheus 名称包括 `dbhub_query_requests_total`、`dbhub_query_request_duration_seconds`、`dbhub_query_executions_total`、`dbhub_query_execution_duration_seconds`、`dbhub_query_timeouts_total`、`dbhub_query_canceled_total`、`dbhub_query_queue_*`、`dbhub_query_metadata_*` 和 `dbhub_query_process_*`。所有结果标签使用固定枚举，指标基数不随 SQL、用户或连接数增长。

## 在 Cloud 中建立基线

先启动本地 Cloud 服务，再运行：

```sh
./scripts/cloud/benchmark.sh
```

应用、压测器及依赖均在 Docker 中。压测器运行在独立临时容器，复用已有代理、CA、Go 缓存与测试环境变量，不打印凭据。默认报告写入私有状态目录下 `benchmark-artifacts/query-baseline-<UTC时间>.json`，文件权限 0600，不覆盖已有文件。报告记录源码提交及工作区是否有未提交修改。

压测器拒绝非 development/test 的后端，创建随机命名的四个临时 developer 账号、两个独立连接，以及 PostgreSQL/MySQL 各一张 10000 行测试表。数据准备使用参数绑定，查询通过真实认证和执行 API，逐行核对 ID、数值和标签。结束后删除测试表、连接和账号并撤销会话；审计和管理员建立/清理对象的防重登记沿用保留规则。清理失败会令报告无效，并附临时命名空间用于排查。

默认运行 27 个阶段：

1. 每种数据库分别执行主键单行读取、主键范围 100 行读取；每种负载测试 1/4/8/16/32 客户端并发，每阶段 5 秒。
2. 每种数据库各执行一次 64 并发受控慢查询过载、一次真实执行超时和一次客户端取消。
3. PostgreSQL 再验证一次真实 30 秒排队超时：仅延长临时账号的执行超时，保持两个受控慢查询占用该用户槽位，验证第三个请求排队超时且未执行，然后取消占用者。

每种负载先预热，每阶段确认无其他执行，阶段间等待请求和队列排空；以 500ms 间隔采样运行指标及数据库连接数。阶段完成后对请求数、完成结果、执行次数和超时/取消原因进行对账。响应错误、数据校验失败、实例改变、采样失败或存在额外执行流量均令该阶段无效，不发布为有效基线。

较短的工具验证可使用：

```sh
./scripts/cloud/benchmark.sh --duration=2s --rows=1000 --queue-timeout=false
```

`--duration` 支持 2–30 秒，`--rows` 支持 1000–100000。省略排队超时探测的运行只有 26 个阶段，应与完整基线区分。

## 阅读结果

- 客户端采用闭环、无思考时间模型：一个请求完成后才发下一次。成功 RPS、尝试 RPS 分开统计；成功、429 拒绝和全部请求分别计算延迟。P50/P95/P99 使用客户端样本的 nearest-rank，拒绝请求的快速响应不会拉低成功 P95。故障探测没有成功响应时，成功延迟的 count 为 0。
- `queue_wait_p95_upper_ms` 是阶段直方图差值算出的桶上界，不是精确分位数；平均排队时长来自该阶段累计和差值。
- CPU 百分比来自后端进程累计 CPU 秒的差值，100% 代表一个逻辑核心，可以超过 100%。GOMAXPROCS 不等于容器 CPU 硬限额。Go 堆与 RSS 分开记录，后者包含非 Go 堆内存。
- PostgreSQL 连接数覆盖测试数据库的客户端连接；Cloud 中它同时承载元数据库和目标数据，因此包含两者及观测器。MySQL 记录服务端连接数及运行线程，包含观测器。连接峰值是采样观测值，不是证明从未出现更高瞬时值。
- 单进程仍为每用户 2、全局 8 个执行，执行和等待合计每用户 10、全局 100 个，等待 30 秒。多个后端进程的指标和限额各自独立。
- 这是本地合成、预热数据的开发基线。结果受 SQL、数据分布、TLS、硬件、日志、连接建立和数据库部署影响，不能据此承诺生产吞吐或实际业务 SLO，也不代替大表、锁竞争及长时间混合负载测试。

验证命令：

```sh
./scripts/cloud/dev.sh exec -T backend sh -ec 'go test -race -p=4 ./...; go vet ./...'
```

指标测试覆盖累积桶、并发计数、权限隔离、重复请求与 SQL 次数区分、真实超时/取消、持久化未知窗口、存储故障及 Prometheus 文本。容量工具测试验证 nearest-rank 和阶段差值分位数，真实压测另外核对所有查询结果、队列释放、过载及故障指标。
