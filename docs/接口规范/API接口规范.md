# 接口规范 (API Specification)

本文按接口查阅具体请求、响应、错误及认证规则；当前阶段、启用边界和未验收事项统一见[路线图](../项目管控/开发路线图与当前任务.md)。关联业务规则见[数据执行契约](../架构设计/数据接入与执行契约.md)和[内容契约](../架构设计/内容与交付契约.md)，目录见[项目文档](../README.md)。

## 公共约定

- 空间业务接口的推荐前缀为 `/api/v1/workspaces/{workspace_id}`，下文以相对路径表示；已登记的 `/api/v1/{业务路径}` 是兼容入口，仍须解析明确的原空间。路径与 `X-Workspace-ID` 冲突返回400；缺空间可能返回428，不自动切到其他空间。
- `/api/v1/auth/*`、获准的 `/api/v1/platform/*`、本人收紧回执和治理恢复是独立身份入口，不要求借用业务空间资格。平台身份不自动获得空间数据权。
- 每个接口同时检查当前模式、实际路由许可、主体、空间、对象和数据边界。下文admin/developer/readonly及四项职责只描述旧兼容路径；已确认的新模型主体按当前许可判定，不用旧admin兜底。
- JSON通常返回 `{code,message,data}`；成功code=0，下载和Prometheus等明确格式除外。写请求按各自DTO严格校验，未知字段和尾随JSON拒绝，不从示例推导未列能力。

| HTTP / code | 行为 |
| --- | --- |
| 400 / 40000 | 请求格式、字段或不支持的能力 |
| 401 / 40100 | 未认证、令牌/会话失效 |
| 403 / 40300 | 当前资格不足 |
| 404 / 40400 | 不存在或当前不可见，不用错误泄漏受限对象 |
| 409 / 40900 | 版本、基线、幂等内容或引用冲突 |
| 428 | 需要明确工作空间等前置条件，具体响应按接口 |
| 429 / 42900 | 登录/准入限流，按响应处理Retry-After |
| 500 / 50000 | 内部错误，仅返回通用提示 |
| 503 / 50300 | 会话、授权、审计或依赖暂不可用，不降级放行 |

现行路由及细项见[routes.json](../../backend/internal/iam/routes.json)和[router.go](../../backend/internal/server/router.go)，信封及错误映射见[httpx](../../backend/internal/httpx/response.go)。字段以对应处理器和前端API类型核对；本文不重复枚举全部路由，也不定义尚未交付的新协议。

## 身份与会话

| 方法与绝对路径 | 请求与响应 |
| --- | --- |
| POST `/api/v1/auth/login` | `{username,password}`；正常返回token_type、access_token、refresh_token、expires_at、user |
| POST `/api/v1/auth/mfa/verify` | `{challenge_token,code,recovery_code}`，使用有效验证码或恢复码完成二次验证；成功才签发会话 |
| POST `/api/v1/auth/refresh` | `{refresh_token}`；重新校验当前身份，成功返回新令牌对；旧刷新令牌不可复用 |
| GET `/api/v1/auth/me` | 当前身份摘要；令牌里的旧角色/职责不是当前授权凭证 |
| GET `/api/v1/auth/mfa`；POST `/api/v1/auth/mfa/setup`、`/api/v1/auth/mfa/enable`、`/api/v1/auth/mfa/disable` | 实际方法分别为GET状态、POST初始化/启用/关闭；密码、setup_id、code及恢复码按对应动作DTO提交 |
| GET `/api/v1/auth/sso`、`/api/v1/auth/sso/start`、`/api/v1/auth/sso/callback`；POST `/api/v1/auth/sso/complete` | 已配置SSO/OIDC的信息、跳转和一次性登录交接；仍进入当前身份与MFA检查，不凭外部组名直接获业务权 |
| POST `/api/v1/auth/change-password` | `{old_password,new_password}`；核对原密码，新旧不能相同；会话失效按当前认证版本处理 |

启用MFA的登录先返回 `{mfa_required:true,challenge_token,expires_in:300}`，此时没有访问或刷新令牌。真实外部身份环境仍按路线图待验，不将路由存在视为SSO产品验收。请求/响应完整类型见[auth](../../backend/internal/auth/)、[adminapi](../../backend/internal/adminapi/)及[前端身份状态](../../frontend/src/stores/user.ts)。

Access Token优先取Authorization Bearer，其次X-Access-Token，再取Cookie dbhub_access_token；不通过URL查询参数传令牌。只用Cookie认证的修改请求须携带X-DBHub-CSRF: 1。令牌有效期按JWT_ACCESS_TTL_MINUTES/JWT_REFRESH_TTL_HOURS配置，默认15分钟/7天；失效或刷新失败不能保活旧权，依赖故障503也不能被误报为密码错误。

现有前端可在401后刷新并至多重放一次；带幂等编号的请求必须沿用原编号。取消、断网或结果未知时先核实原回执，不能换编号重发写入。令牌持久化细节复用[请求封装](../../frontend/src/utils/request.ts)，不要求所有客户端复制同一存储方式。

GET `/api/health`为进程存活，GET `/api/ready`检查关键依赖；均无需业务认证。进程存活不等于可用、产品验收或获准启用。

## 数据源管理 (Connections)

> 除标注外均需 Access Token（三通道任一）。`/connections` 表示当前用户的个人连接，所有登录角色均可创建、修改、删除和测试自己的连接；管理员也不例外。数据库执行仍独立检查平台角色和实际数据库账号权限。
> 公共目标通过 `/datasource-definitions` 发布，不包含用户名或密码。使用者确认修订并配置个人身份后才能操作。其他用户的个人连接统一不可访问。完整流程与迁移见[共享数据源与个人连接](../架构设计/数据接入与执行契约.md#1-数据源身份与能力)。

### 连接列表
**GET** `/connections?type=postgres&keyword=prod`

**Response data:**
```json
{
  "items": [
    {
      "id": 1, "user_id": 1, "ssh_tunnel_id": null,
      "name": "订单库", "type": "mysql", "host": "10.0.0.11", "port": 3306,
      "database": "orders", "username": "app", "ssl_mode": "require",
      "connection_timeout": 10, "color_label": "", "environment": "prod",
      "has_password": true, "tunnel_name": "",
      "created_at": "2026-09-15T04:00:00Z", "updated_at": "2026-09-15T04:00:00Z"
    }
  ],
  "total": 1
}
```
> 接口**绝不回传口令明文或密文**，仅返回 `has_password` 布尔标志。

### 创建连接
**POST** `/connections`
```json
{
  "name": "订单库", "type": "mysql", "host": "10.0.0.11", "port": 3306,
  "database": "orders", "username": "app", "password": "<数据库口令>",
  "ssh_tunnel_id": null, "ssl_mode": "require",
  "connection_timeout": 10, "color_label": "", "environment": "prod"
}
```
`type` 仅允许 `mysql | postgres | redis`；口令经 AES-256-GCM 加密后落库。
`environment` 可选，仅允许 `dev | test | prod`，缺省回落 `dev`。成功返回标准信封与连接详情。

### 修改连接
**PUT** `/connections/{id}`，请求体同创建。`password` 留空表示保留原口令不修改。

### 删除连接
**DELETE** `/connections/{id}`：仅本人；有引用时遵守服务端保护，相关历史按当前外键及留存规则处理，不保证任意级联删除。

### 测试连接
- **POST** `/connections/test`：请求体同创建（用于保存前验证，口令不落库）
- **POST** `/connections/{id}/test`：空请求体使用已保存配置测试；携带完整连接配置时测试编辑草稿

草稿测试仅限该个人连接的所有者，所有登录角色均可使用。目标与账号未变化时，口令留空复用本人已保存口令；修改数据库类型、主机、端口、数据库、账号、TLS 或代理/SSH 隧道后，必须重新输入口令。草稿测试不修改配置或已保存配置的检查记录。

**Response data:**
```json
{
  "ok": true,
  "type": "postgres",
  "version": "PostgreSQL 16.15 ...",
  "elapsed_ms": 48,
  "started_at": "2026-10-17T10:00:00Z",
  "checked_at": "2026-10-17T10:00:00.048Z",
  "recorded": true,
  "compatibility": {
    "engine": "postgres",
    "product": "PostgreSQL",
    "version": "16.15",
    "branch": "16",
    "status": "verified",
    "tested_version": "16.15",
    "message": "该版本已通过基础兼容验证。"
  }
}
```
失败返回 HTTP 400 / `40000`，消息为清洗后的驱动错误（不含口令片段），`data` 包含 `ok=false`、`error`、耗时与检查时间。保存配置的成功/失败检查都会持久化最近一条，列表/详情以可选 `last_check` 返回。`recorded=false` 表示草稿测试，或该结果因配置变更/较新检查而未记录；配置修改后旧检查失效，删除连接时级联删除记录。详情见[检查与采集可靠性](../架构设计/数据接入与执行契约.md#4-检查采集与持久任务)。

`version` 保留原有响应字段；`compatibility.version` 为规范化版本。`ok` 表示连接成功，兼容结论另见 `status`：`verified` 为实测补丁；`branch_verified` 为同分支其他补丁；`unverified` 为尚未验证的版本、兼容产品或 Redis 部署模式；`unknown` 为版本无法可靠识别。Redis 账号可以连接但无权读取 INFO 时返回成功与 `unknown`，不伪造验证结论。Redis 的 `compatibility.mode` 表示服务端报告的运行模式。具体边界见[数据源版本兼容](../架构设计/数据接入与执行契约.md#4-检查采集与持久任务)。

### 版本验证范围

**GET** `/connections/compatibility`，所有已登录角色可读，未登录返回 `401`。本接口只读取内嵌验证记录，不连接目标数据库。

**Response data（节选）:**
```json
{
  "items": [
    { "engine": "mysql", "branch": "5.7", "tested_version": "5.7.44" },
    { "engine": "postgres", "branch": "16", "tested_version": "16.15" }
  ],
  "scope": "基础 SQL、元数据和 Redis 单节点键值操作；不保证所有新语法或兼容产品"
}
```

完整记录含 12 个分支；只有非空 `tested_version` 才表示已有实测。连接列表本身不自动探测版本，显示的是用户最近一次针对当前已保存配置的检查。

---

## SSH与代理

SSH隧道和代理都是当前本人网络配置，不能读取或借用其他人的凭据。SSH兼容接口继续有效，按实际连接器能力选择。

| 方法与相对路径 | 请求、响应及边界 |
| --- | --- |
| GET/POST `/ssh-tunnels`；PUT/DELETE `/ssh-tunnels/{id}` | 名称、host、port、username、auth_type及对应password/private_key/passphrase；只返回凭据已配置标志 |
| POST `/ssh-tunnels/test` | 保存前配置，按当前公钥/TLS及目标限制验证；返回ok或清洗后的错误 |
| GET/POST `/proxies`；PUT/DELETE `/proxies/{id}` | name、type、host、port、username、password、description及status；type仅http/https/socks5/db_proxy，custom返回400 |
| POST `/proxies/test` | 已保存本人id或待测配置；HTTP/HTTPS CONNECT及SOCKS5需target_host/target_port，数据库代理按其网关端点测试；成功 `{ok:true,latency_ms}` |

测试执行真实拨号，受DBHUB_TARGET_ALLOWLIST及10秒超时限制，成功响应表示本次拨号完成。被引用对象删除按当前引用保护处理；代理配置存在不表示所有连接器支持经该代理访问。JDBC的网络能力须另外核对。完整DTO及校验见[proxyapi](../../backend/internal/proxyapi/handler.go)、[SSH处理器](../../backend/internal/datasource/handler.go)和[数据接入契约](../架构设计/数据接入与执行契约.md#1-数据源身份与能力)。

## SQL 工作台 (Query & Metadata)

### 执行 SQL
**POST** `/query/execute`
```json
{ "request_id": "d504b64a-cd47-42f9-8714-bde634f013ea", "connection_id": 1, "database": "orders", "sql": "SELECT id, name FROM users LIMIT 100" }
```
查询类（SELECT / WITH / SHOW / DESC / EXPLAIN / TABLE / VALUES）返回：
```json
{
  "kind": "query",
  "columns": ["id", "name"],
  "rows": [[1, "alice"]],
  "truncated": false,
  "duration_ms": 12
}
```
写入/DDL 类返回：`{ "kind": "write", "affected_rows": 2, "duration_ms": 5 }`。

约束：
- `request_id` 必填，使用非空 UUID。同一次请求的网络重放须沿用原编号；用户主动发起新的执行时生成新编号。工作台客户端已自动生成；直接调用 API 的客户端必须同步升级。
- 以用户和请求编号为唯一键，在 PostgreSQL 元数据库中原子登记。已登记编号再次提交返回 HTTP 409，`data` 为原执行回执；不同 SQL、参数、目标、选项或角色不得复用同一编号。失败、超时、进程退出均不释放编号，服务端不会自动重跑。
- 一次只允许执行一条语句（多语句返回 `40000`，防止批量注入）；
- 默认最多返回 **1000 行**，超出 `truncated=true`；默认超时 30 秒。`max_rows`、`timeout_ms` 服从用户保存的更严格限制；硬上限分别为 10000 行、120 秒。返回行数限制不限制更新或删除的影响行数。
- `readonly` 角色同时使用 SQL AST 检查和数据库只读事务。默认禁止没有 WHERE、或 WHERE 明显恒真的 UPDATE/DELETE；支持识别 `TRUE`、`1=1`、`OR 1=1` 等字面量条件，不证明任意条件的影响范围。
- 普通执行使用独立连接；仅显式传入 `transaction_id` 时使用已有事务。事务内语句成功不表示事务已提交。
- 执行完成后记录历史；请求登记和执行回执不保存 SQL、参数或查询结果行，不提供结果缓存。

**GET** `/query/executions/{request_id}`：查询本人执行回执，其他用户（含管理员）不能跨用户读取。示例 `data`：

```json
{
  "request_id": "d504b64a-cd47-42f9-8714-bde634f013ea",
  "connection_id": 1,
  "database": "orders",
  "status": "succeeded",
  "transactional": false,
  "kind": "write",
  "affected_rows": 1,
  "duration_ms": 5
}
```

`status` 为 `queued`、`running`、`succeeded`、`failed`、`rejected` 或 `unknown`。`rejected` 表示未发送用户 SQL；`unknown` 表示结果不确定，不得解释为写入回滚。执行错误响应也可能携带回执，客户端应优先使用回执状态。元数据库暂时不可用时返回 503；重复编号返回 409；排队过载返回 429。未找到回执的 404 也不能单独证明原请求未执行。

取消、超时、断网后只查询回执，不自动重新提交 SQL。超过 5 分钟未完成的排队/执行记录展示为 `unknown`，保持编号占用。回执不保存查询行，确认查询完成后只显示完成摘要。完整一致性与部署边界见 [执行可靠性](../架构设计/数据接入与执行契约.md#3-发送前登记回执和未知结果)。

### 取消与显式事务

| 方法与相对路径 | 请求及边界 |
| --- | --- |
| POST `/query/jobs/{id}/cancel` | 仅本人、原空间的在途任务；响应表示取消请求处理，不证明原SQL已撤回，按原request_id继续核实 |
| POST `/query/transaction/begin` | connection_id、database；返回transaction_id和当前状态 |
| POST `/query/transaction/commit`、`/query/transaction/rollback` | connection_id、transaction_id；归属固定本人和目标会话，确认丢失不能自动重试提交或重放SQL |
| GET `/query/transaction/status` | connection_id；只返回当前本人事务状态，语句完成不等于已提交 |

完整载荷及状态见[workbench.ts](../../frontend/src/api/workbench.ts)和[jobs.go](../../backend/internal/queryapi/jobs.go)。这些既有高级接口不要求恢复所有早期工作台面板。

### 对象浏览（均为 GET）
| 接口 | 说明 |
| --- | --- |
| `/metadata/databases?connection_id=` | 逻辑库列表（自动过滤系统库） |
| `/metadata/schemas?connection_id=&database=` | PostgreSQL 模式列表 |
| `/metadata/tables?connection_id=&database=&schema=` | 表与视图（`type=table/view`） |
| `/metadata/columns?connection_id=&database=&schema=&table=` | 列结构（类型/可空/主键/默认值/序号） |

### 表数据分页预览
**GET** `/data/preview?connection_id=1&database=orders&schema=public&table=users&page=1&page_size=50`
```json
{ "columns": ["id", "name"], "rows": [[1, "示例记录"]], "total": 1, "page": 1, "page_size": 50, "has_more": false }
```
> 表名/模式名按引擎验证并安全引用（PG双引号、MySQL反引号），不能直接拼接未验证的标识符。

### Redis 浏览（GET）
| 接口 | 说明 |
| --- | --- |
| `/redis/overview?connection_id=` | 版本/模式/运行天数/客户端数/内存/键空间统计 |
| `/redis/keys?connection_id=&pattern=user:*&limit=200` | SCAN 游标遍历键（默认上限 500），含类型与 TTL |
| `/redis/value?connection_id=&key=k` | 按类型预览值（string/list/hash/set/zset，大元素截断 100 项） |

### 查询历史
- **GET** `/query/history?connection_id=&status=success|failed&page=1&page_size=20`
  → `{ items, total, page, page_size }`，所有角色均仅本人当前可见记录；
- **DELETE** `/query/history/{id}`：删除本人单条记录；
- **DELETE** `/query/history`：清空本人历史；管理员不清空他人记录。

---

## 旧用户与角色管理

> 此为旧用户/角色兼容管理接口，须满足当前管理许可及目标范围；不授予他人个人凭据、历史或私有文件读取权。

| 方法与路径 | 说明 |
| --- | --- |
| GET `/roles` | 内置角色列表（admin/developer/readonly） |
| GET `/users?keyword=&page=&page_size=` | 用户分页列表（含角色、状态、最近登录） |
| POST `/users` | 创建用户 `{username(>=3), password(>=8), role, email?}`，重名返回 `40900` |
| PUT `/users/{id}` | 修改角色/邮箱（不能降低自己角色；须保留至少一个启用管理员） |
| PATCH `/users/{id}/status` | 启用/停用 `{is_active}`（不能停用自己） |
| POST `/users/{id}/reset-password` | 管理员重置密码 `{new_password}` |
| DELETE `/users/{id}` | 删除用户（不能删除自己；级联清理其连接/历史） |

### 兼容管理边界
本人改密见“身份与会话”，不依赖空间管理许可。

旧角色与职责的兼容边界统一见[逐主体映射](../架构设计/数据接入与执行契约.md#72-旧权限逐主体兼容)；新自定义角色使用IAM接口，旧users/roles接口不承载新角色许可。旧角色/职责变更受到迁移屏障约束，不得绕过当前收紧。

## 操作审计

**GET** `/audit-logs?days=7&username=&action=&resource_type=&status=success|failed&page=&page_size=`

关键写操作（登录、改密、连接/隧道/用户增删改、测试连接、SQL 执行、历史清理）
由可靠审计登记和幂等补写，记录操作者、来源 IP、User-Agent、结果状态、耗时与 trace_id；
请求体不落库以防泄密。返回 `{ items, total, page, page_size }`。
`action` 取值：`LOGIN / CHANGE_PASSWORD / CREATE / UPDATE / DELETE / TEST / QUERY`。

## 仪表盘指标（Metrics）

### 仪表盘汇总

**GET** `/metrics/overview?days=14`

按当前运行读取许可及数据范围访问；`days` 取 1~90，缺省/越界均回落到 14。连接、查询历史和数据目录按当前用户返回；管理员的运行指标与操作审计另行授权。慢查询统计按[history_repo.go](../../backend/internal/db/history_repo.go)中的阈值及对应查询定义，不在文档另设性能门槛。

响应data含kpi、connection_types、trend、rank和recent，各字段含义如下。

字段说明：

- `kpi.connections`：数据源总数（已纳管）；`today_queries`：当日 0 点起的查询次数；
  `today_active_users`：当日执行过查询的去重用户数；`audit_events`：审计事件累计数。
- `connection_types`：按 `mysql / postgres / redis` 分组计数，未使用的类型可能缺键。
- `trend`：按天补齐的时间序列（无数据日补 0），`total/failed/slow` 分别为查询总量、
  失败量、慢查询量。
- `rank`：时间窗口内各数据源查询量降序，最多 8 条。
- `recent`：最近查询记录，最多 8 条，结构同查询历史列表项。

### SQL执行运行指标

**GET** `/metrics/query`，或 `/metrics/query?format=prometheus`。

需要当前运行指标读取资格；旧兼容路径限管理员。未登录401，缺许可403。默认返回标准 JSON 信封；
`format=json` 等同默认，`format=prometheus` 返回 Prometheus 0.0.4 文本，其他格式返回 400。
两种格式均设置 `Cache-Control: no-store`，不包含 SQL、参数、用户、连接或请求编号标签。

JSON 的 `data` 包含 `started_at`、`captured_at`、`uptime_seconds`、`requests`、
`executions`、`counters`、`queue`、`process`、`metadata_pool` 和 `receipt_store_available`：

- `requests` 统计进入执行处理器的请求，包含重复、冲突及拒绝；`executions` 仅统计实际开始的用户 SQL。
  两者分别提供 `total`、`in_flight` 和按固定结果分类的 `completed` 耗时直方图，单位为秒。
- `counters` 统计超时、取消、登记失败和回执写入失败。超时/取消是原因计数，可与请求结果重叠。
- `queue` 提供运行/等待数量、进程峰值、接纳/拒绝/等待超时计数、等待耗时和当前执行限额；
  `process` 提供 Go 堆、goroutine，Linux 另提供累计进程 CPU 秒和 RSS；`metadata_pool` 提供元数据库连接池占用与等待。
- 数据库可读时附 `receipts_last_24h`，汇总近 24 小时创建的 queued/running/unknown，
  超过 5 分钟未完成的记录计入 unknown，`stale` 是其子集。此处 unknown 是历史回执数量，不等于用户当前待办数。
  汇总不可读时仍返回 200 及本进程指标，`receipt_store_available=false` 并省略持久化数量。

进程计数和峰值在重启后清零，以 `started_at` 识别实例；多实例各自统计。
当前指标结构见[metrics](../../backend/internal/metrics/)，仅表达实际采集信息；不恢复已移出的性能测试或门槛。

---

## 数据目录

接口受当前路由细项及元数据/预览/治理权限分别约束，菜单可见不授予完整读取权。快照由采集产生，人工标注独立保存，重新采集不覆盖标注。路径均使用当前空间前缀。

| 方法与相对路径 | 请求与响应 |
| --- | --- |
| POST `/assets/sync` | 兼容同步入口，空体或{}表示当前获准源；单源为{connection_id}，须正整数；响应items含各源status及发布计数，errors聚合失败 |
| GET `/assets/overview` | 当前可见资产统计，不返回跨空间/他人对象的数量 |
| GET `/assets/tree?connection_id=` | 当前可见来源、库/Schema、表树及计数；MySQL以库对应Schema，PostgreSQL过滤系统Schema；空树不证明目标持续在线 |
| GET `/assets/tables` | connection_id、database、schema、q、type、sensitivity、no_owner、starred、page/page_size；返回items、total、page、page_size，page_size默认50、上限200 |
| GET `/assets/table` | connection_id、database、schema、table定位；返回结构、标注、字段、索引及可用元数据，未知目标404 |
| PUT `/assets/annotations` | connection_id/database/table必填，column空表示表级；business_desc最多1000字、tags最多8个且每个20字；owner须当前合法启用成员 |
| POST `/assets/star` | 对象定位及star布尔；本人幂等收藏，返回starred |
| GET `/assets/search?q=` | 少于2字符返回空分组；返回connections/tables/columns/history及保留的reports分组；表/字段最多8条，历史最多6条，历史仅本人，reports当前保留为空 |
| GET `/users/brief` | 当前可列举成员摘要，受上下文及目录资格限制，不返回认证材料 |

sensitivity仍为旧normal/sensitive/confidential标注，不等于阶段2统一分类与输出规则已执行。统计热度沿现有历史聚合，不另建独立热度模型；字段检索复用已有快照。完整返回字段见[assetapi](../../backend/internal/assetapi/handler.go)及[元数据仓储](../../backend/internal/db/metadata_repo.go)。

当前页面使用持久源任务，接口见“任务、通知与固定业务办理”；同步兼容接口串行采集、整体4分钟。每源全部规定库/Schema读取完成后原子发布和清理，发布前失败保留原快照。同源并发互斥：单源冲突409，批量标busy后继续。status为succeeded/failed/busy/skipped/unknown，提交确认丢失标unknown，刷新核实且不自动重试。JDBC完整采集单独检查collect，Redis等不支持路径标skipped。

## 个人报表与旧仪表盘

> 此为个人定义兼容接口，执行使用查看者本人的匹配连接，不是正式受管内容。读取按当前可见性和动作许可，修改/删除仅本人；收藏与编辑分别检查，管理员不绕过私有边界。

### 创建报表

**POST** `/reports`（写角色，记审计）

```json
{
  "name": "月度销售趋势",
  "description": "按月聚合",
  "connection_id": 1,
  "database": "postgres",
  "sql": "SELECT date_trunc('month', created_at) AS month, SUM(total_amount) FROM shop.orders GROUP BY 1 ORDER BY 1",
  "chart_type": "bar",
  "chart_config": {
    "dimension": "month",
    "metrics": ["sum"],
    "aggregation": "sum",
    "sort": "asc",
    "topN": 20
  },
  "visibility": "private"
}
```

- `name` 必填 ≤128，`sql` 必填单语句，`chart_type`按当前[图表定义](../../frontend/src/components/business/chart/ChartCard.vue)校验，`visibility ∈ private|shared`
- `chart_config` 为 ChartCard 配置：维度/指标/聚合/排序/TopN
- 成功返回报表详情，含 `id/owner_user_id/created_at` 等

### 报表列表

**GET** `/reports?q=&scope=mine|starred|shared|all&connection_id=&page=1&page_size=20`

- `scope=mine` 仅本人，`starred` 仅收藏，`shared` 仅共享，`all` 全部（私有仅本人可见）
- 响应 `{ items: [Report], total, page, page_size }`，Report：
```json
{
  "id": 1, "name": "月度销售", "description": "", "connection_id": 1, "database_name": "postgres",
  "sql_text": "SELECT ...", "chart_type": "bar", "chart_config": {"dimension":"month","metrics":["sum"]},
  "visibility": "private", "owner_user_id": 1, "owner_name": "admin",
  "starred_by": [1], "starred": true,
  "created_at": "2026-09-20T10:00:00Z", "updated_at": "2026-09-20T10:00:00Z"
}
```

### 报表详情 / 更新 / 删除

- **GET** `/reports/{id}`：只返回当前可见个人定义，私有仅本人
- **PUT** `/reports/{id}`：仅本人可编辑，请求体同创建（修改需携带当前version）
- **DELETE** `/reports/{id}`：仅本人

### 报表收藏

**POST** `/reports/{id}/star` `{ "star": true|false }` → `{ "starred": true }`，任意登录用户，幂等

### 仪表盘 CRUD

> 读：登录可用；写：`admin`/`developer`；私有仅 owner 可见；收藏任意登录用户。

- **POST** `/dashboards` `{ name(必填≤128), description?, visibility?: private|shared, layout?: [{report_id,x,y,w,h}] }` → `{ id }`
  - `layout` 为 12 栅格：`w` 1~12，`x/y/h` 可选，这是旧layout模型，当前正式编排使用managed-dashboards的tiles，不能混用
- **GET** `/dashboards?q=&scope=mine|starred|shared|all&visibility=&page=&page_size=` → `{ items, total }`
  - `scope` 同报表；`items[].layout` 为原始 JSON；`starred` 布尔投影
- **GET** `/dashboards/{id}`：私有校验本人
- **PUT** `/dashboards/{id}`：仅本人，支持改名/描述/可见性/布局
- **DELETE** `/dashboards/{id}`：仅本人，删除范围由服务端当前引用及所有权规则确定，不由前端清理扩大授权
- **POST** `/dashboards/{id}/star` `{ starred: bool }` → `{ starred }`

Dashboard 响应示例：

```json
{
  "id": 1, "name": "销售总览", "description": "月度指标",
  "visibility": "shared", "layout": [{"report_id":1,"x":0,"y":0,"w":6,"h":4},{"report_id":2,"x":6,"y":0,"w":6,"h":4}],
  "owner_user_id": 1, "owner_name": "admin",
  "starred_by": [1], "starred": true,
  "created_at": "2026-09-20T10:00:00Z", "updated_at": "2026-09-20T10:00:00Z"
}
```

### 公开分享的兼容边界

公开分享已停用。POST /shares 返回403；GET /public/s/{token} 返回404，不返回内容。历史分享的登录后列表、吊销和删除接口仅用于管理旧记录，不构成重新启用公开读取的入口。当前应登录后按本人或正式受管执行路径检查权限，行为见[share_handler.go](../../backend/internal/reportapi/share_handler.go)。

## 数据资产血缘（Lineage）

### 读取与刷新

- **GET** `/assets/lineage?connection_id=&database=&schema=&table=&depth=1|2|3|all`
  - `depth` 1 为直接依赖，2 为二度，all 为全量；响应：
```json
{
  "nodes": [
    { "id":"postgres.orders.customers","name":"customers","db":"orders","type":"table" },
    { "id":"postgres.orders.orders","name":"orders","db":"orders","type":"table" }
  ],
  "edges": [
    { "from":"postgres.orders.customers","to":"postgres.orders.orders","kind":"fk" },
    { "from":"postgres.orders.orders","to":"postgres.orders.order_items","kind":"sql" }
  ],
  "impact": { "upstream":2, "downstream":3, "downstream_reports":1, "downstream_dashboards":1 }
}
```
  - `kind ∈ fk（外键实线）| sql（SQL 依赖虚线）| report（仪表盘引用点线）`
  - 合并实时快照外键与 `sys_query_history` 解析（`pg_query_go` / `vitess/sqlparser`）+ 仪表盘引用，缓存表 `sys_meta_lineage`；优先读取缓存，未命中时按已有快照、历史和报表实时计算。

POST /assets/lineage/refresh 提供刷新入口。读取接口需登录并通过当前路由许可及连接范围检查；table为空返回400，目标不存在或未同步返回404。depth缺省为2，all按3层处理。响应另含upstream、downstream和cached；节点可能包含表、视图、报表及仪表盘。实现见[lineageapi](../../backend/internal/lineageapi/)，不以示例图证明依赖完整或跨空间可见。

## 工单中心（Tickets）

工单已落库。申请人及管理员可查看；developer 可以为自己的个人连接提交变更，readonly 不可提交。审批和实际执行均限管理员。

| 接口 | 载荷或响应 |
| --- | --- |
| GET `/tickets?status=&q=&type=&page=&page_size=` | `{items,total,page,page_size}`，按可见范围过滤 |
| GET `/tickets/{id}` | 工单详情及执行摘要 |
| POST `/tickets` | `{title,type,table,database,connection_id,sql,risk,reason}`，返回工单；必须指定本人的连接 |
| POST `/tickets/{id}/status` | `{"status":"approved"}` 或 `{"status":"rejected"}`，仅能处理 pending |
| POST `/tickets/{id}/execute` | 原子领取 approved 工单，使用执行者本人对相同全局修订的连接，重复领取返回冲突 |

工单不接受 SELECT。全局目标的 `database` 不能覆盖发布的数据库。执行结果仅保留 `kind`、`affected_rows`、`duration_ms`，不提供执行者身份下的结果行或列。状态为 `pending|approved|rejected|executing|executed|failed`；失败或不确定执行不会自动重试。旧共享连接工单的迁移规则见文末 0022 说明。

资产血缘入口可通过 `/tickets?table=&db=&title=&sql=&risk=` 预填变更申请。

---

## 连接器目录、自定义数据源与能力

认证与响应沿用公共约定。驱动和连接规则支持 `visibility=private|global`，新增默认 private。旧兼容路径允许登录用户登记已安装制品的本人配置；当前管理者按路由许可及来源范围发布和管理公共版本，旧兼容为admin。visibility=global只表示当前空间公共，不授予跨空间读取或平台管理。严格 JSON、审计和引用保护保留，不返回路径或凭据。

| 方法/相对路径 | 行为 |
| --- | --- |
| GET /connector-drivers/available | 登录用户读取 JDBC 服务实际可加载、校验通过的制品 |
| GET /connector-drivers | 已登记驱动摘要列表 |
| POST /connector-drivers | 登记 `{ "artifact_id":"postgresql-42.7.7", "name":"PostgreSQL JDBC", "visibility":"private" }`；同范围重复制品 409 |
| PATCH /connector-drivers/{id}/status | 本人所有者或当前空间获准管理者 `{ "enabled": false }`；停用阻止新会话 |
| DELETE /connector-drivers/{id} | 本人所有者或当前空间获准管理者 删除未引用版本，引用中返回 409 |
| GET /datasource-templates | 读取当前有权模板；管理者可在合法范围包含停用版本 |
| POST /datasource-templates | 本人所有者或当前空间获准管理者 发布首个不可变修订 |
| PUT /datasource-templates/{id} | 本人所有者或当前空间获准管理者 按相同 key 和范围追加修订，不改旧实例 |
| PATCH /datasource-templates/{id}/status | 本人所有者或当前空间获准管理者 启停指定修订 |
| DELETE /datasource-templates/{id} | 本人所有者或当前空间获准管理者 删除未引用修订，引用中返回 409 |
| GET /connections/{id}/capabilities | 取得配置绑定和实际实现的能力，不触发目标 SQL |

模板请求示例：

```json
{"template_key":"pg-jdbc","name":"PostgreSQL JDBC","product":"PostgreSQL","driver_id":1,"dialect":"postgres","url_template":"jdbc:postgresql://{host}:{port}/{database}","fields":[{"name":"host","label":"主机","type":"string","required":true},{"name":"port","label":"端口","type":"integer","required":true,"default":"5432"},{"name":"database","label":"数据库","type":"string","required":true}]}
```

fields 只定义 URL 占位参数，不接受凭据、任意 JDBC 属性或执行脚本。驱动对应可校验的 URL 结构；未知驱动须先加入部署制品清单；通用网络模板要求包含 `//{host}:{port}`，只提供结构浏览，不启用完整采集。特殊 URL、认证和范围映射需额外适配。`dialect` 默认为空，仅元数据；mysql/postgres 方言须与实际驱动家族匹配。URL 不含用户名和密码。缺字段、未知字段、非法 URL/模板返回 400；目标类型或传输能力未实现返回 400；制品或服务不可用返回 503；不存在返回 404；版本重复/被引用返回 409；无权限返回 403。

JDBC 实例沿用 /connections CRUD 和测试接口，扩展如下字段：

```json
{"name":"分析库","type":"jdbc","template_revision_id":1,"parameters":{"host":"postgres","port":"5432","database":"analytics"},"host":"postgres","port":5432,"database":"analytics","username":"analyst","password":"<only-in-request>","ssl_mode":"disable","connection_timeout":10,"environment":"test"}
```

实例的 host/port/database 从模板参数规范化，不以重复字段覆盖模板。JDBC 第一版支持直连；PostgreSQL/MySQL 已验证 TLS 的可信 CA、主机名检查与失败关闭。H2 内存演示及未知产品不开放 TLS 选项，代理和 SSH 明确拒绝。编辑留空可复用口令，但模板、参数、身份、TLS 等目标信息变化时草稿测试要求重输；保存后检查按配置修订失效。实例响应增加 template_revision_id、parameters、template 摘要、capabilities，不返回口令。`type=jdbc` 只是兼容字段，产品、驱动、方言来自绑定的模板。

能力响应示例：

```json
{"code":0,"message":"ok","data":{"sql":true,"format":true,"metadata":true,"collect":true,"keys":false,"transactions":true,"enforced_read_only":true}}
```

JDBC 查询沿用 /query/execute 防重与回执，不自动重发 SQL；精确小数和大整数以无损文本返回，二进制以显式编码返回，响应可附列类型。JDBC 的连接结果与兼容性分开，原生矩阵不自动授予 JDBC 已验证状态。元数据不支持的 DDL/统计由 metadata_status 标识，不以 0 或空文本冒充已读取；采集仍整轮原子发布。

结构采集的 `status` 为 `succeeded|failed|busy|skipped|unknown`。`unknown` 表示元数据库提交确认丢失，可能已经发布；客户端提示刷新数据目录核实，不能将其表述为失败并保留旧快照。JDBC 默认只对实例配置库提供元数据，响应 `metadata_status` 记录不支持的 DDL/统计；完整采集单独检查 `collect`。

## 共享数据源定义与个人绑定

下列路径使用空间前缀，需要登录及当前来源管理/绑定资格。定义和凭据分别存储；公共定义写入须当前管理许可及来源范围；个人定义仍检查本人所有权。

| 路由 | 行为 |
| --- | --- |
| GET `/datasource-definitions` | 全局及本人私有的最新可见修订，附当前用户 `my_connection_id`、`my_revision_id` |
| GET `/datasource-definitions/{id}/revisions?page=1&page_size=20` | 可见历史修订、相对上一可见版本的 `changes`；管理者另获 `binding_count`；每页最大 100 |
| POST `/datasource-definitions` | `{name,description,visibility,config}`；config 只允许目标字段，拒绝认证字段 |
| POST `/datasource-definitions/{id}/revisions` | 追加修订；旧绑定不变，相同内容的重复发布返回已有修订 |
| PATCH `/datasource-definitions/{id}/status` | 启用 `{enabled:true}`；停用 `{enabled:false,confirmed:true,expected_binding_count:N}`，人数变化返回 409；仅影响指定修订的新操作 |
| POST `/connections/{id}/publish` | 从本人的连接复制去凭据定义，`{visibility,description}`；代理与 SSH 暂不随定义发布 |
| POST `/datasource-definitions/{id}/connections` | `{name,username,password,confirmed:true}`；创建个人绑定，重复返回 409 |
| POST `/datasource-definitions/{id}/connections?test=true` | 同样的个人身份载荷，测试但不保存或改写检查记录 |
| POST `/connections/{id}/revision` | `{definition_revision_id,name,username,password,confirmed:true}`；确认同一逻辑源的修订并重新输入凭据 |

`/connections` 响应新增 `definition_revision_id`、`definition_family_id` 和 `definition` 摘要。全局关系型定义须指定库或服务；数据库选择和采集固定到发布的库，不能通过 API 覆盖。编辑公共绑定不能覆盖目标字段；修改身份或目标会使旧采集与检查失效。无数据库认证的 Redis 可保留空密码，关系型连接须明确填写自己的账号密码。

公开查询结果与报表分享入口关闭，旧令牌迁移撤销。共享报表读取时使用查看者对同一发布修订的个人连接；未配置时 `connection_id=0`，客户端提示配置。管理员审批和执行工单时也需自己的匹配连接。管理员不能下载他人的导出或查看他人的私有查询任务与历史。

0022 将引用他人连接的 pending/approved 旧工单改为 rejected，要求以个人连接重新提交，并将历史执行结果收敛为不含结果行的摘要。

连接检查失败的 `data.diagnostic` 增加固定的 `{code,title,hint,action}`。已保存检查会持久化，草稿检查不保存；成功时不返回失败诊断。分类包括认证、权限、数据库不存在、TLS、DNS、网络、超时、容量、驱动服务、目标策略、配置、取消和未知原因，不回显底层异常。完整分类和修订管理契约见[数据源修订与连接诊断](../架构设计/数据接入与执行契约.md#1-数据源身份与能力)。

生产环境必须配置 `DBHUB_TARGET_ALLOWLIST`；未获允许的目标、代理及 SSH 端点不可连接。被驱动配置或其他资源引用的用户删除返回 409，可先停用用户并处理配置归属。

## 正式报表与仪表盘

以下为当前受管内容接口，独立于个人reports和旧dashboards。业务与账号边界、草稿/冻结审批/发布三个维度、参数及数据契约见[内容与交付契约](../架构设计/内容与交付契约.md)，不把阶段3新多节点策略当作已实现。

| 方法与相对路径 | 关键请求及响应 |
| --- | --- |
| POST `/managed-reports`；PUT `/managed-reports/{id}/draft` | 报表定义及request_id，修改含version；返回id/version |
| POST `/managed-reports/{id}/submit` | version、request_id；冻结内容并建立审批记录 |
| GET `/managed-reports/{id}/versions`、`/managed-reports/{id}/releases` | 分页版本/审批与追加发布事件，分别保留真实状态及版本号 |
| POST `/report-reviews/{id}/validate` | account_id、user_ids、group_ids、params及request_id；返回result、validation_id、manifest_hash |
| POST `/report-reviews/{id}/decision` | decision、validation_id、comment、contract_confirmed、request_id；批准须本人15分钟内且当前账号/受众等基线一致的验证，不可自审 |
| POST `/managed-reports/{id}/releases` | expected_publication_no、review_id、kind及request_id；publish/rollback/unpublish按当前资格及依赖检查 |
| POST `/managed-reports/{id}/transfer` | version、owner_id、request_id；责任人移交不转赠凭据或旧签名 |
| GET/POST `/report-accounts`；PATCH `/report-accounts/{id}`；POST `/report-accounts/{id}/test` | 创建含name/definition_revision_id/username/password/request_id；维护按version轮换密码或启停并确认影响；返回裁剪账号摘要，测试返回connected/read_only |
| GET `/published-reports`、`/published-reports/{id}`、`/metric-catalog` | 仅当前获准展示信息，不含SQL、目标地址及执行账号配置 |
| POST `/published-reports/{id}/execute` | publication_no、request_id、params；禁止覆盖SQL、目标、账号和事务 |
| POST `/managed-dashboards`；PUT `/managed-dashboards/{id}/draft` | definition、request_id，修改含version；definition含tiles、user_ids/group_ids、quality及parameters |
| POST `/managed-dashboards/{id}/preflight` | version；返回当前依赖及受众覆盖预检，不能作为长期授权凭证 |
| GET `/managed-dashboards/{id}/versions`、`/dashboard-reviews/{id}/summary` | 当前编排版本、独立审批摘要及实际差异 |
| GET `/published-dashboards`、`/published-dashboards/{id}` | 当前获准看板及逐卡ready/restricted/contract_changed说明 |
| POST `/managed-dashboards/{id}/refreshes` | mode、review_id、params；返回refresh_id及固定本轮清单 |
| POST `/dashboard-refreshes/{id}/tiles/{tile}/execute` | 原刷新及卡片；返回本轮绑定结果、execution_id和quality，不按客户端新目标重解释 |

报表定义的parameters固定名称、类型、必填、默认值及允许范围；执行params只填已批准参数，不能拼入SQL或改变目标。data_contract的业务语义、结果结构、单位、范围和freshness随冻结版本维护；普通数值/NULL及水位按批准定义返回。

仪表盘tiles绑定report_id、contract_hash、required、comparison_group及follow_published，固定历史依赖和仪表盘文件/周期交付尚未开放。访问申请、仅调整受众、发布/归档/恢复等动作沿现有专用接口和版本控制，完整载荷见[managed-report.ts](../../frontend/src/api/managed-report.ts)、[managed-dashboard.ts](../../frontend/src/api/managed-dashboard.ts)及[处理器](../../backend/internal/reportapi/)。不把通用action字符串视为允许任意操作的协议。

同一命令编号换操作或内容返回409，未知状态先核实原记录。报告/看板审批、源状态、账号代次、当前受众及发布号每次重查；普通查看与审批预览分别授权。

## 报表导出、个人订阅与告警

本节描述已有接口。旧文件/期次保持7天；新24小时文件、10分钟本人领取凭证、90/30天复核及团队逐人交付是[阶段4规则](../架构设计/内容与交付契约.md#8-已确认的后续内容与交付规则)，当前未启用。

| 方法与相对路径 | 关键请求与响应 |
| --- | --- |
| POST `/published-reports/{id}/exports` | publication_no、request_id、format、reason、params；format为csv/json/xlsx，返回导出申请、版本、状态和期限 |
| GET `/report-exports`、`/report-exports/{id}`、`/report-exports/{id}/preview` | 当前本人或合法复核范围的申请摘要、裁剪审批预览；不能借此读取他人完整文件 |
| POST `/report-exports/{id}/review` | version、decision、comment；独立当前复核，冲突409 |
| GET `/report-exports/{id}/download` | 返回获批文件二进制；编码后及实际返回前重查原主体、到期时间、审批版本和快照校验值 |
| GET/POST `/report-subscriptions`；PUT `/report-subscriptions/{id}` | definition、request_id，修改含version；definition固定report_id/publication_no、interval_minutes、params、alert_rule、webhook及enabled |
| POST `/report-subscriptions/{id}/runs`、`/report-subscriptions/{id}/archive` | version、request_id；手动期次或归档，不能自动换成另一个发布号 |
| GET `/report-deliveries`、`/report-deliveries/{id}` | 本人期次及快照、状态、原发布号和数据时间；读取不变成重新执行 |
| GET `/metric-incidents`、`/metric-incidents/{id}` | 当前获准告警、发生值及处置事件 |
| POST `/metric-incidents/{id}/actions` | version、action、comment、request_id，action为acknowledge/comment/close；保留原发生值 |

当前正式导出申请生成审批快照，批准控制领取；不冒充“批准后才生成新正式文件”。export_policy控制是否开放及max_rows，服从当前更严格限制，快照16MiB上限、精度及公式文本保护保留。Webhook仅使用已配置端点与获准载荷，不在API接收任意外发URL。

完整DTO及错误见[report-exports.ts](../../frontend/src/api/report-exports.ts)、[report-subscriptions.ts](../../frontend/src/api/report-subscriptions.ts)、[导出处理器](../../backend/internal/reportapi/exports.go)、[订阅处理器](../../backend/internal/reportapi/subscriptions.go)。

## 任务、通知与固定业务办理

| 方法与相对路径 | 关键请求与响应 |
| --- | --- |
| GET `/source-tasks`、`/source-tasks/{id}` | 本人任务、原空间、状态、阶段及裁剪结果 |
| POST `/source-tasks`；POST `/source-tasks/{id}/retry` | 创建含connection_id、kind=check/collect、request_id；重试含新request_id并保留旧任务，不自动重放 |
| POST `/source-tasks/{id}/cancel` | 排队取消立即处理，运行中尽力取消，不代表已回滚已提交快照 |
| GET/PUT `/source-schedules` | 保存connection_id、kind、interval_minutes、enabled、version；返回当前本人计划 |
| GET `/source-check-history` | 原配置修订下的检查记录，不能显示为持续在线 |
| GET `/inbox`、`/inbox/todos`；POST `/inbox/read` | 本人通知及实时待办，单条或列表高水位已读；已读不等于业务处理完成 |
| GET `/business-operations/templates`、`/business-operations/accounts` | 当前可办理模板或有权维护的账号摘要 |
| POST `/business-operations/previews` | template_id、version、target_key、amount；返回原目标版本、前后摘要及预览编号 |
| POST `/business-operations/requests` | preview_id、reason、request_id；返回申请id/version |
| POST `/business-operations/requests/{id}/validate`、`/business-operations/requests/{id}/decision` | 当前version、核验结果及独立决定；批准含validation_id、confirmed和request_id |
| POST `/business-operations/requests/{id}/execute`、`/business-operations/requests/{id}/reconcile` | 执行含version/confirmed，核实沿原operation_id；返回原申请状态及裁剪回执，unknown不得自动重跑 |

任务容量、租约、最短周期及主体约束见[数据执行契约](../架构设计/数据接入与执行契约.md#4-检查采集与持久任务)，通知与留存见[内容契约](../架构设计/内容与交付契约.md#6-导出订阅和通知)。固定业务办理不接收任意SQL，与数据库变更工单分别授权；完整载荷及状态见[automation.ts](../../frontend/src/api/automation.ts)、[business-operations.ts](../../frontend/src/api/business-operations.ts)及[业务处理器](../../backend/internal/businessops/)。

平台运行状态使用独立绝对路径GET `/api/v1/platform/operations`，需要平台资格；不能通过选择一个空间获得平台运维权限。

## 工作空间底座诊断

`GET /api/v1/workspaces/{workspace}/iam/foundation`要求登录及本空间当前有效成员资格，只返回调用者本人。路径与`X-Workspace-ID`显式冲突返回400；无效/冻结/到期成员返回404；权限存储不可用或授权版本记录缺失返回503。无空间选择的兼容调用可以返回428，不自动切到其他空间。

正常响应沿用`{code,message,data}`，data包含：

| 字段 | 含义 |
| --- | --- |
| `stage`、`activation_ready` | 固定为`FOUNDATION_ONLY`、`false`，不代表完整权限运行桥已经验收 |
| `catalog_version`、`runtime_bridge` | 固定目录版本和当前底座标识 |
| `snapshot` | 本人/原空间、当前成员/配置/授权代次、撤权序号、旧数据身份、原职责来源及期限 |
| `functional_envelope` | 动作、允许/拒绝、原因、明确来源；每项均带`requires_object_check=true` |
| `notice` | 对象、数据、字段、审批和任务继续按原路径校验的说明 |

功能上限说明不是资源、业务数据、审批或文件领取授权；客户端不能拿快照设置自己的角色或可信版本。JWT中的IAM与职责字段不被信任，服务端每次重新绑定当前成员。旧admin仅保留固定四职责，未知新许可拒绝；旧四值输入格式不承载新具名许可。

foundation是兼容诊断接口；当前治理实现见下节。`foundation`保留旧兼容诊断契约，其固定标志不能代替当前有效权限或配置包启用状态。现有成员变化记录及代次持久化与该变化处于同一事务。冻结记录不会因原成员恢复active或普通配置模式变更而消失。

职责中间件的拒绝审计记录`AUTH_DENIED`及`auth_failure=insufficient_capability`；原角色守卫仍可记录`insufficient_role`。两者均不得输出凭据。

## 空间权限与治理

以下空间治理接口前缀为`/api/v1/workspaces/{workspace}`。接口在当前空间重新绑定成员、旧数据身份、配置和授权代次，客户端角色或版本不构成授权。默认真实环境保持旧模式；前端新页面开关`VITE_IAM_UI_ENABLED`默认关闭。隔离样例中的角色与数据不代表真实成员已迁移。

| 方法与后缀 | 用途与限制 |
| --- | --- |
| GET `/iam/effective`、`/iam/catalog` | 本人当前许可、合法来源、期限、数据身份、菜单及运行中已启用的细项；功能许可仍须经过对象和原数据约束检查 |
| GET `/iam/roles`、`assignments`、`delegations`、`groups`、`members` | 按各细项查看空间治理目录；不能据此读取个人凭据或业务结果 |
| GET `/iam/assignable-roles`、`assignment-targets` | 仅返回本人当前有效委派可处理的角色和成员，不以全量目录权限作为选择器前提 |
| GET `/iam/proposals`、`own-proposals`、`custody` | 治理申请或本人申请、冻结后的待交接资产摘要；业务审批和原签名不改写 |
| POST `/iam/proposals/preview`、`/iam/proposals` | 预览和提交角色定义、角色分配、组增加、委派、限期解除禁止或隔离包启用；均不立即扩权 |
| POST `/iam/proposals/review` | 审批精确变更；申请人与全部受益人回避，同时核实当前复核资格、委派范围及原基线 |
| POST `/iam/restrictions` | 冻结成员、停用角色、撤销角色来源或委派、禁止功能操作；限制与递增代次先原子持久化 |
| GET `/iam/restrictions` | 查询当前治理委派目标与许可范围内的最新操作禁止、限期解除和审批历史；需要`space.assign.view`，不提供任意成员的禁止目录 |
| GET/PUT `/iam/menus` | 11个稳定目录的完整显示配置；只支持名称、分类、顺序、显示和推荐首页，不接受任意路由或动作 |
| PUT `/iam/home` | 保存本人在当前空间可见且有权访问的菜单首页 |
| GET `/iam/resources`、`/iam/policy` | 当前原资源、原业务审批策略的只读说明；新增数据范围、多节点业务审批策略在后续阶段实施 |
| GET `/iam/routes`、`/iam/migration/bridge` | 实际展开的有限接口规则与当前运行版本核对，不作为授权凭证 |
| POST `/iam/migration/shadow` | 元数据只读对照，不取目标数据库业务数据、不导出、不送达消息 |
| POST `/iam/bundles`、`/iam/bundles/rollback` | 准备配置包或执行兼容回退；仅服务器与数据库双重登记的隔离环境可使用；启用还需独立审批和当前运行桥核对 |

治理写入使用UUID `request_id`、明确的`delegation_id`与`delegation_version`、当前`baseline={config_version,authorization_epoch}`以及精确目标版本。相同请求与相同内容返回原回执；同编号换内容或基线变化返回冲突。有限委派同时约束治理动作、可授许可、目标成员、数据源及期限；本人可执行不等于可授予。

撤权持久化成功与在途执行结束是两个状态：`state=applied`表示限制已保存，新授权检查使用新代次；`execution_fence=confirmed`才表示已核实兼容执行边界。无法立即取得执行屏障或运行版本证明时返回HTTP 202、`code=0`和`execution_fence=pending`，不得据此宣称在途写事务已撤回。原请求编号及已产生的业务回执保持，不自动重发写操作。普通HTTP成功回执与此202都不能代替业务最终结果。

以下接口仅要求当前身份，不依赖仍有效的空间成员资格：

- GET `/api/v1/iam-command-status`及`/{workspace}/{request}`：只返回本人提交的收紧回执；冻结或退出原空间后仍可核实执行边界，不返回他人目录或凭据。
- GET `/api/v1/iam-recovery`、POST `/api/v1/iam-recovery/requests`及`/api/v1/iam-recovery/verifications`：原空间治理恢复。两名不同预登记身份各自实际核验；MFA已启用时仍需MFA。通过后还需重新登录，最多15分钟，仅限治理，不能恢复业务查看、个人连接、原角色来源或旧订阅。

角色名称自由配置；多来源合并不会提高原数据身份。未知主体保持完整旧兼容路径，TARGET拒绝后不会回退旧admin。本人安全、通知及获准平台入口独立于11个空间菜单；隐藏菜单不撤权，实际撤许可后URL与API均拒绝。新菜单加载失败或原空间失效时，不自动进入其他空间。

配置回退不删除增量表、撤权账和审计，不恢复冻结、到期或已撤销来源；原业务签名、发布号、7天文件期限和结果未知回执持续保留。真实启用须按路线图另行明确授权；当前仅有隔离实现与技术证据，产品和独立验收仍分开记录。

### 限期解除操作禁止

使用上述治理提案接口，`patch.kind=action_release`，明确填写`subject_id`（成员）、`action_id`、`revocation_sequence`（当前最新禁止记录）、`reason`和`valid_until`。使用原有`space.assign.manage`及有限委派中的`assignment.propose/assignment.review`；不新增许可编号、通配目标或数据资源授权。

提交与批准均核对当前空间基线、最新禁止记录、受益成员、许可启用状态和双方委派的目标、许可与期限上限。申请人及受益人不能复核自己的解除；批准、记录及审计原子提交，相同请求只返回原结果。数据库同时检查解除记录与已生效独立提案的精确对应，解除及原禁止历史均保留。

解除只在获批期限内覆盖该记录及更早的同成员同操作禁止；到期后禁止自动恢复，后来新增的禁止立即优先。解除不重建已撤销的角色来源、不恢复冻结成员、不提高原数据身份，也不解除其他工作空间的限制。到期后再次解除必须提交新的独立提案。页面位于当前空间「角色分配 → 禁止操作与解除审批」，待复核时明确显示仍禁止，审核在「治理审批」完成。
