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
| 499 / 49900 | 非治理业务响应交付前请求上下文已取消；不交付缓冲结果，不表示权限变化或写入回滚 |
| 504 / 50400 | 非治理业务响应交付前请求上下文截止时间已到；不交付缓冲结果，不自动重试 |
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

`GET /api/v1/auth/sso/start` 可接收可选 `redirect`，值为以单个 `/` 开头的站内路径，可附带查询和片段。服务端将有效值保存在同次短期一次性授权状态。回跳消费有效状态后，返回配置的 DBHub 登录地址，并在 `sso` 标记之外带回 `redirect`。无效地址或无有效授权状态时省略该参数。身份提供方的回调地址继续使用部署配置，页面导航沿用现有工作空间和权限核验。

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
- 发送前发现 SQL 语法错误时直接返回 HTTP 400。原生 MySQL 查询收到数据库明确的语法、字段不存在、表不存在或对象查询权限拒绝（1142 / 42000）时返回 HTTP 400，`data.status` 为 `failed`，`message` 给出可展示的错误类型。结果无法确定时仍返回待确认回执并保留原执行编号。
- 默认最多返回 **1000 行**，超出 `truncated=true`；默认超时 30 秒。`max_rows`、`timeout_ms` 服从用户保存的更严格限制；硬上限分别为 10000 行、120 秒。返回行数限制不限制更新或删除的影响行数。
- PostgreSQL 原生连接的标量 `xml` 值作为原文字符串返回，包括 `EXPLAIN (FORMAT XML)`。`NULL` 保留为空值，二进制 `bytea` 沿用 Base64 编码。
- `readonly` 角色同时使用 SQL AST 检查和数据库只读事务。默认禁止没有 WHERE、或 WHERE 明显恒真的 UPDATE/DELETE；支持识别 `TRUE`、`1=1`、`OR 1=1` 等字面量条件，不证明任意条件的影响范围。
- 普通执行使用独立连接；仅显式传入 `transaction_id` 时使用已有事务。事务内语句成功不表示事务已提交。
- 执行完成后记录历史；请求登记和执行回执不保存 SQL、参数或查询结果行，不提供结果缓存。
- 普通 SQL 执行的结果交付因权限复核失败而停止时返回 HTTP 409 / `40902`，响应不携带执行数据。已确认成功的持久回执保持 `succeeded`。调用方保留原编号，日后仍须具备当前空间和回执许可才可读取。

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

执行与查询回执的 `data.cancellation_confirmed` 为可选布尔值，仅在 `failed` 或 `rejected` 终态且服务端确认原请求因取消而未完成时返回 `true`。历史回执和缺少确认的回执省略该字段。客户端按实际回执呈现成功、失败、已取消或结果待确认，发起取消及客户端连接中断本身均不能用来确认终态或回滚。

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

PC 全局数据目录以下两项沿用登录令牌、当前工作空间和 `catalog.metadata` 状态校验，并复用现有数据库列表与字段接口。

| GET 路径 | 响应及异常 |
| --- | --- |
| `/metadata/catalog/sources` | `{ items: [{ id, name, type }] }`，仅返回本人在当前空间可访问且具备元数据浏览能力的连接。存储故障返回 500。 |
| `/metadata/catalog/objects?connection_id=&database=` | `{ items: [{ schema, name, type, comment }], incomplete: boolean }`。复用同一现有授权会话列出该库各模式的表和视图。个别模式不可读取时返回可读对象及 `incomplete: true`，全库读取失败返回错误。连接或数据库参数非法返回 400。 |

两项接口均按现行认证规则返回 401 或 403。无权使用的连接沿现有连接错误规则返回 403 或 404。目录无行数据、无连接凭据、无查询执行回执。

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
| `/redis/keys?connection_id=&pattern=user:*&limit=200` | SCAN 游标遍历键（默认上限 200），含类型与 TTL |
| `/redis/value?connection_id=&key=k` | 按类型预览值（string/list/hash/set/zset），列表和有序集合最多读取 10000 项 |

键详情沿用当前工作空间来源授权。请求在获准来源内确认指定键已不存在时返回 HTTP 404、业务码 `40401`。来源缺失和读取失败沿用各自错误码。

### 查询历史
- **GET** `/query/history?connection_id=&status=success|failed&page=1&page_size=20`
  → `{ items, total, page, page_size }`，所有角色均仅本人当前可见记录；
- **DELETE** `/query/history/{id}`：删除本人单条记录；
- **DELETE** `/query/history`：清空本人历史；管理员不清空他人记录。

<a id="workbench-34-api"></a>

### 工作台脚本与结果接口

接口对应 [#34 编码与验收计划](../项目管控/开发路线图与当前任务.md#workbench-34-must-plan)。路径沿用 `/api/v1/workspaces/{workspace}` 的工作空间路由，主体验证沿用现有登录会话和空间权限中间件。请求字段及响应位于现有接口包络内。工作空间和所有者从已认证请求解析。

| 方法和相对路径 | 请求与成功响应 |
| --- | --- |
| GET `/query/script-folders` | 返回本人目录 `{ items: [{ id, parent_id, name }] }`。顶层目录的 `parent_id` 为空。 |
| POST `/query/script-folders` | `{ name, parent_id }`，返回创建后的目录。上级须属于本人当前空间。 |
| PATCH `/query/script-folders/{id}` | `{ name }`，返回重命名后的目录。 |
| GET `/query/scripts?view=mine\|shared&folder_id=&q=&page=&page_size=` | `{ items, total, page, page_size }`。列表项含 `id, name, script_type, updated_at, read_only`。本人列表含 `folder_id, shared`，共享列表含分享者显示名。保持当前筛选和稳定排序。 |
| POST `/query/scripts` | `{ name, folder_id, script_type, content, default_target? }`。创建后返回完整本人脚本。未设绑定时 `default_target` 为空。另存副本走创建路径，创建者为当前主体，新对象独立管理授权。 |
| GET `/query/scripts/{id}` | 返回该编号已保存的 `content` 和列表项字段。本人含 `version, default_target`；共享视图含 `read_only=true`，并以当前读权限即时判定。 |
| PATCH `/query/scripts/{id}` | `{ version, name?, content?, folder_id?, default_target? }`。省略字段保留当前值，`folder_id: null` 移到根目录，`default_target: null` 解除默认绑定。返回保存后的脚本及新版本。 |
| GET `/query/scripts/{id}/readers`、GET `/query/scripts/{id}/share-candidates?q=` | 本人脚本可分别读取有效查看名单和当前空间内可选成员。只返回选择所需的成员标识与显示名。 |
| PUT、DELETE `/query/scripts/{id}/readers/{user_id}` | 所有者逐人授予或撤回只读访问。重复同一动作返回当前状态 `{ user_id, granted }`，名册变更和审计关联同一次写入。 |

`script_type` 创建时取 `mysql`、`postgres`、`redis`，保存、移动和分享均引用同一脚本标识。本人写入有效默认目标时提交 `{ connection_id, database }`。服务端核对本人连接当前状态、实际方言和工作台能力。Redis 的 `database` 使用连接配置的固定逻辑库，未显式配置时为 `0`，变动后须重新确认。读取本人详情时，失效绑定以可识别的 `state=unavailable` 返回，界面保留脚本文本用于续编。共享详情仅返回安全的脚本数据和分享者信息。目录、脚本正文、数据库执行分别按各自资格检查。

请求格式、类型或目标不匹配返回 400，未认证返回 401。不可访问的脚本或目录返回 404，已具备只读访问但请求写入或转授返回 403。版本冲突返回 409，保留客户端内容供用户处理。服务不可用按既有错误包络返回。34.3 随旧片段清理收口 `/snippets` 路由、权限目录和前端旧封装。脚本操作统一走本节接口，旧路径的兼容写入关闭。原接口有关越权和请求体限制的测试迁到新入口。

结果相关接口补充适用下列要求。

- 普通工作台 `/query/execute` 的查询结果把 PostgreSQL 超出 JavaScript 安全整数范围的整数及精确小数以原始数值文本交付，普通值、NULL、XML 和二进制沿用当前表示。只调整受影响的工作台传输和显示，统一保真规则前核对其他调用者和报表契约。精确值尚无法安全映射到图表时可保留表格原值并给出明确反馈。
- 从本人已保存脚本运行时，可附 `script_id`。服务端在发送 SQL 前复核当前脚本、本人工作空间和实际目标的类型及执行能力。实际执行使用本次显式提交的 SQL 和已选目标，沿用申请身份、执行编号、防重和回执。原自助查询入口继续接受合法的独立 SQL。
- `/redis/value` 的 list、zset 响应补充布尔 `truncated`，基于同次有界读取的额外一项判断。响应的值最多交付 10,000 项，刚好 10,000 项时为 `false`。键已不存在、读取失败与成功截断分别显示。
- 查询历史复用本人接口。现有历史可能只保存 SQL 前缀，实现时对截断或无法确认完整性的记录给出提示，载入不完整文本供续编时保持未执行状态，由用户检查后主动运行。

---

## 平台账号与工作空间管理（#43）

矩阵空间的直接角色管理使用会话认证。GET `/role-assignments/workspaces/{workspace}/members` 读取 `{workspace_id,workspace_name,enabled,roles,members}`。角色含 `code,name,actions`，成员含 `user_id,username,member_role,version,role_codes`。PUT `/role-assignments/workspaces/{workspace}/members/{member}` 接收 `{request_id,expected_version,role_codes}`，返回更新后成员。服务端核对目标工作空间管理员或平台超管资格。重复请求使用原回执，版本冲突返回 409，范围或资格不足返回 403，无法找到目标返回 404，未认证返回 401。

平台角色目录使用会话及超管认证。GET `/platform/role-templates` 返回 `{items}`，每项含 `id,code,name,actions`，适用已纳入矩阵的空间，无工作空间参数。非超管返回 403，未认证返回 401。工作空间菜单在平台页面按明确目标调用原 `/workspaces/{workspace}/iam/effective`、`/workspaces/{workspace}/iam/roles` 和 `/workspaces/{workspace}/iam/menus`。读取与保存核对目标空间现行资格，保存沿用当前基线、菜单版本、请求编号和回执。

矩阵成员管理复用会话与显式目标空间路径 `/role-assignments/workspaces/{workspace}`。GET `/members` 另返回布尔字段 `can_manage_members`、`can_manage_admins`。GET `/member-candidates?keyword=` 返回可加入的当前启用平台账号列表 `[{user_id,username}]`。POST `/members` 以 `{user_id,request_id}` 添加普通成员，初始业务角色为空，已存在成员记录按原重新加入或核对流程处理。PATCH `/members/{member}/membership` 以 `{operation,expected_version,request_id}` 修改成员关系。`operation` 允许 `remove`、`appoint_admin`、`revoke_admin`。添加或移出普通成员由目标空间管理员或超管办理，空间管理员的直接角色写入也限本空间普通成员。任免只限超管，至少保留一名有效空间管理员。成功返回含 `active` 的实际成员回执，重复请求返回原回执。无效输入返回 400，未认证返回 401，越权或受保护目标返回 403，找不到对象返回 404，版本、成员状态或最后管理员冲突返回 409。所有变更与空间审计同事务提交。

新模式受管报表详情包含 `business_owner_id`、`business_owner_name`、`business_owner_version` 和相应送审、审核、换任能力。POST `/managed-reports/{id}/submit` 在首次指定主管时接受 `business_owner_id`。GET `/managed-reports/{id}/business-owner-candidates` 返回 `{items:[{user_id,username}]}`，供技术维护人首发选择或超管更换。PUT `/managed-reports/{id}/business-owner` 接收 `{request_id,business_owner_id,expected_version}`，返回报表编号和最新负责人代次。前置资格或活动边界变化拒绝，版本冲突 409，资格不足 403。现有预览、审批、数据审批和发布接口继续使用原版本约束。

以下管理接口使用当前身份登录，不依赖用户所选工作空间。服务端核对有效的平台超级管理员资格。未登录返回 401，无资格返回 403，输入不完整返回 400，记录不存在返回 404，版本不一致或身份唯一性冲突按原错误信封返回 409。

| 方法及地址（`/api/v1` 下） | 请求和成功响应 |
| --- | --- |
| GET `/platform/accounts` | `keyword`、`status`（`pending/active/inactive`）、`login_method`（`local/sso`）、`page` 和 `page_size`。返回 `{items,total}`；各项提供账号标识、用户名、邮箱、状态、平台资格、本地与 SSO 登录方式、多因素状态、最近登录、创建时间和当前版本。 |
| POST `/platform/accounts` | `{username,email,reason}`。创建无空间资格的普通待激活账号。返回 HTTP 201 及 `{account,credential:{token,purpose,expires_at}}`。 |
| POST `/platform/accounts/{id}/credential` | `{purpose,expected_version,reason}`，`purpose` 为 `activate/reset`。返回新凭据及账号，旧未使用凭据失效。密码维护使用本地凭据及对应资格。 |
| PATCH `/platform/accounts/{id}/status` | `{active,expected_version,reason}`。返回最新账号。停用使旧会话失效，恢复需重新登录，拒绝停用本人或最后可用的超级管理员。 |
| POST `/platform/accounts/{id}/revoke-sessions` | `{expected_version,reason}`。撤销旧会话并返回最新账号。 |
| GET `/platform/workspaces` | `keyword`、`page` 和 `page_size`。返回 `{items,total}`，项包括空间标识、名称及创建元信息，能够确认时返回首任管理员。无业务对象内容。 |

`POST /auth/account-credential` 由本人以 `{token,password}` 完成一次性设置，不依赖旧会话。凭据使用服务端摘要保存，激活和重置均为 15 分钟有效、一次兑换。失效、过期或已使用统一拒绝，重置使旧会话失效。一次性明文仅存在于创建凭据的当次响应和本人提交的请求体。正式平台页面开通空间继续使用 `POST /workspaces`，请求明确追加 `permission_matrix:true`。旧客户端省略时沿用原创建方式，不改变真实权限模式。身份资格、凭据原因和流程结果按平台审计规范记录，凭据秘密不进审计。

## 旧用户与角色管理

> 此为旧用户/角色兼容管理接口，须满足当前管理许可及目标范围；不授予他人个人凭据、历史或私有文件读取权。

| 方法与路径 | 说明 |
| --- | --- |
| GET `/roles` | 内置角色列表（admin/developer/readonly） |
| GET `/users?keyword=&page=&page_size=` | 用户分页列表（含角色、状态、最近登录） |
| POST `/users` | 创建用户 `{username(>=3), password(>=8), role, email?}`，重名返回 `40900` |
| PUT `/users/{id}` | 修改当前工作空间角色，按全局身份许可选改邮箱。请求 `{role, email?}`，确认后返回 `{id}` |
| PATCH `/users/{id}/status` | 启用/停用 `{is_active}`（不能停用自己） |
| POST `/users/{id}/reset-password` | 管理员重置密码 `{new_password}` |
| DELETE `/users/{id}` | 删除用户（不能删除自己；级联清理其连接/历史） |

### 兼容管理边界
本人改密见“身份与会话”，不依赖空间管理许可。

用户目录的 `can_edit_email` 说明当前操作者能否通过兼容入口修改该用户的邮箱。角色编辑仍要求当前工作空间管理许可，保留本人降级与最后一位启用管理员限制。邮箱属于全局身份，修改沿用兼容身份操作的范围，操作者须具有平台管理资格，目标仅属于当前工作空间。省略邮箱或提交当前值时只编辑角色，有许可时显式提交空字符串可清空邮箱。角色与邮箱在同一事务确认。认证沿用当前工作空间的登录会话。目标不存在返回 `40400`，修改邮箱无许可时返回 `40300`，角色不合法或触发管理员保护时返回 `40000`。

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
| GET `/assets/elements` | 分页返回当前空间、本人可见连接的已采集技术元素。支持 `q`（最多 200 字）、`connection_id`、重复参数 `type`、`level`、`tag` 及 `page`、`page_size`（默认 24，上限 100）。详情内浏览相关元素时可传精确位置 `connection_id`、`database`、`schema`、`table` 和目标类型。 |
| GET `/assets/tables` | connection_id、database、schema、q、type、sensitivity、no_owner、starred、page/page_size；返回items、total、page、page_size，page_size默认50、上限200 |
| GET `/assets/table` | connection_id、database、schema、table定位；返回结构、标注、字段、索引及可用元数据，未知目标404 |
| PUT `/assets/annotations` | connection_id/database/table必填，column空表示表级；business_desc最多1000字、tags最多8个且每个20字；owner须当前合法启用成员 |
| POST `/assets/star` | 对象定位及star布尔；本人幂等收藏，返回starred |
| GET `/assets/search?q=` | 少于2字符返回空分组；返回connections/tables/columns/history及保留的reports分组；表/字段最多8条，历史最多6条，历史仅本人，reports当前保留为空 |
| GET `/users/brief` | 当前可列举成员摘要，受上下文及目录资格限制，不返回认证材料 |

sensitivity仍为旧normal/sensitive/confidential标注，不等于阶段2统一分类与输出规则已执行。统计热度沿现有历史聚合，不另建独立热度模型；字段检索复用已有快照。完整返回字段见[assetapi](../../backend/internal/assetapi/handler.go)及[元数据仓储](../../backend/internal/db/metadata_repo.go)。

`/assets/elements` 使用 Bearer 会话和当前工作空间，受 `catalog.metadata` 约束。类型为 `database`、`schema`、`table`、`view`、`field`、`index`、`constraint`。分级包含 `normal`、`sensitive`、`confidential`、`unclassified`，字段与表的标注独立。搜索按名称、位置和说明做忽略大小写的字面包含匹配；同组多选取并集，不同条件取交集。返回 `items`、`total`、`page`、`page_size`、`sources` 和 `available_tags`，每行含类型、来源、位置、名称、说明、分级、标签、采集时间及对应结构属性。数据库与 Schema 行从可见快照归并，展示已采集范围。无匹配返回空列表；无效位置参数、筛选和分页返回 400，未登录或无许可分别返回 401、403，读取故障返回 500。接口复用现有快照与标注存储。

当前页面使用持久源任务，接口见“任务、通知与固定业务办理”；同步兼容接口串行采集、整体4分钟。每源全部规定库/Schema读取完成后原子发布和清理，发布前失败保留原快照。同源并发互斥：单源冲突409，批量标busy后继续。status为succeeded/failed/busy/skipped/unknown，提交确认丢失标unknown，刷新核实且不自动重试。JDBC完整采集单独检查collect，Redis等不支持路径标skipped。

## 个人报表与旧仪表盘

> 此为个人定义兼容接口，执行使用查看者本人的匹配连接，不是正式受管内容。读取按当前可见性和动作许可，修改/删除仅本人；收藏与编辑分别检查，管理员不绕过私有边界。

### 创建报表

**POST** `/reports`。使用当前工作空间的 Bearer 会话，需要个人报表写入资格。服务端记审计。

```json
{
  "name": "月度销售趋势",
  "description": "按月聚合",
  "connection_id": 1,
  "database": "postgres",
  "request_id": "7d24d585-bbcd-4ce6-bf83-ff9829266ff7",
  "sql": "SELECT date_trunc('month', created_at) AS month, SUM(total_amount) FROM shop.orders GROUP BY 1 ORDER BY 1",
  "chart_type": "bar",
  "chart_config": {
    "dimension": "month",
    "metrics": ["sum"]
  },
  "visibility": "private"
}
```

- `name` 必填且最多 128 字。`sql` 为单条只读语句。`request_id` 为非空 UUID。`visibility` 支持 `private` 和 `shared`。
- 表格报表可以提交 `chart_config: {}`。持久配置支持维度、指标和适用的展示及图表设置。聚合、排序和筛选由查询定义表达。提交预览的聚合、排序和 TopN 配置会返回 400。
- #45 补充 `chart_type` 值 `horizontal_bar`、`area`、`scatter`、`histogram`、`funnel`、`heatmap`。横向条形、面积沿用 `dimension` 与 `metrics`。散点用 `dimension` 绑定数值 X，`metrics` 唯一字段绑定数值 Y。直方和漏斗用 `dimension` 绑定查询中已排序的区间或阶段，`metrics` 唯一字段分别绑定非负整数频数、非负数值。热力图由 `dimension`、`heatmap_y` 绑定互异的横纵类目，`metrics` 唯一字段绑定色阶数值，查询结果每对坐标唯一。已有 `pie` 可选 `pie_style: "solid" | "donut"`，省略时按历史环形呈现，保留旧配置结构。以上字段都随报表原保存、预览和发布返回，沿用登录、工作空间和报表授权，配置结构不合法返回 400。正式发布的字段映射还要核对数据契约。
- 成功返回 HTTP 200 和报表详情，含 `id`、`version`、`owner_user_id`、`created_at` 等。定义或连接参数无效返回 400，当前连接或源授权不可用返回 403，创建编号与已有定义冲突返回 409。

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

原生 PostgreSQL 的 INSERT、UPDATE 和 DELETE 使用数据库命令标签的影响行数，包括带 `RETURNING` 的语句。执行响应与持久化回执使用同一计数，适用范围见 [A2 工单详情](../界面设计/UI设计规范.md)。

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
| GET `/datasource-definitions` | 全局及本人私有的最新可见修订，附当前用户 `my_connection_id`、`my_revision_id`。`view=shared` 只列当前主体可见的已启用共享修订，搜索及分页照常执行 |
| GET `/datasource-definitions?view=managed` | 按原工作空间身份校验共享定义管理许可，无许可返回 403。每个共享数据源取最新可见修订，含停用。可传 `keyword`（名称或默认库）、`type`（`postgres\|mysql\|redis\|jdbc`）、`environment`（`dev\|test\|prod`）、`status`（`enabled\|disabled`），过滤后返回 `items`、`total`。空值表示不限制，无效选项或分页参数返回 400 |
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

连接检查失败的 `data.diagnostic` 增加固定的 `{code,title,hint,action}`。已保存检查会持久化，草稿检查不保存；成功时不返回失败诊断。分类包括认证、权限、数据库不存在、TLS、DNS、网络、通信中断、超时、容量、驱动服务、目标策略、配置、取消和未知原因，不回显底层异常。`connection_lost` 表示读取EOF、不完整响应或驱动已失效连接等明确断连信号，标题为“连接在通信过程中断开”；不推断错误密码或断连责任方，提示检查协议端点和服务器/代理日志，不自动重试或降低TLS要求。完整分类和修订管理契约见[数据源修订与连接诊断](../架构设计/数据接入与执行契约.md#1-数据源身份与能力)。

生产环境必须配置 `DBHUB_TARGET_ALLOWLIST`；未获允许的目标、代理及 SSH 端点不可连接。被驱动配置或其他资源引用的用户删除返回 409，可先停用用户并处理配置归属。

## P2-01 业务身份与数据集映射

使用现有 Bearer 登录和工作空间绑定。仅当前空间管理员可调用，按数据源治理权限与当前限制检查。请求与响应使用公共 JSON 信封；`PUT` 成功返回与相应 `GET` 相同的对象和新 `version`。

| 请求 | 参数与响应 |
| --- | --- |
| `GET /api/v1/business-datasets/{key}` | 返回数据集键、登记来源、来源版本、有效期、版本、`available` 及受管执行账号 ID、账号版本、固定来源修订 ID。 |
| `PUT /api/v1/business-datasets/{key}` | 对象字段 `expected_version`、`authority`、`source_version`、`valid_from`、`valid_until`、`account_id`、`account_version`。 |
| `GET /api/v1/business-identities/{user}` | `{user}` 为当前空间应用成员 ID。返回 `user_id`、`business_key`、登记来源、来源版本、有效期、版本、`available` 和 `datasets`。数组项含数据集 `key`、已确认的 `version` 与 `available`。 |
| `PUT /api/v1/business-identities/{user}` | 对象字段 `expected_version`、`authority`、`business_key`、`source_version`、`valid_from`、`valid_until`、`datasets`。`datasets` 是 `{key, version}` 数组，必填，空数组明确清除全部绑定。 |

数据集键使用小写字母、数字、点、下划线和连字符，首字符为字母或数字，最多 80 字符。来源、业务键和来源版本均为非空文本，最多 128 字符。有效期用 RFC 3339 时间戳，截止时刻不再有效。`expected_version: 0` 创建，修改提交最近一次读回版本。`account_version` 与映射中 `version` 必须匹配已确认的对象版本。每次身份保存最多提交 100 个数据集。

无效字段或重复数据集键返回 `40000`，缺少登录 `40100`，不具备管理资格 `40300`，当前空间无对应成员或登记记录 `40400`，旧版本、重复业务键、账号或数据集版本过时或不可登记返回 `40900`。无有效空间时沿用工作空间 `40400`/`42800`，基础设施错误沿用公共错误约定。读写纳入现有持久审计，记录对象标识和结果。响应只含元数据，映射不会授予数据动作或返回账号凭据。

## P2-02 部门、仓库与运输组织范围

沿用 P2-01 的 Bearer 登录、空间管理员资格、数据源治理权限、工作空间隔离、审计和公共响应信封。路径为 `/api/v1/business-identities/{user}/datasets/{key}/organization-scope`。

| 方法 | 请求与响应 |
| --- | --- |
| GET | 返回 `user_id`、`dataset_key`、`identity_version`、`dataset_version`、`version`、登记来源和有效期、`scopes`（已登记组合）、`effective_scopes`（当前可用组合）。 |
| PUT | 请求字段为 `expected_version`、`identity_version`、`dataset_version`、`authority`、`source_version`、`valid_from`、`valid_until` 和 `scopes`，成功返回与 GET 相同的字段及新版本。 |

`scopes` 为必填数组，最多 100 条。每条可含 `department_key`、`warehouse_key`、`transport_org_key`，至少一个非空业务键。业务键两端不得有空白，最多 128 字符，按精确值记录。空数组收回组织范围。缺省维度不会被展开为通配。保存先检查身份和数据集的绑定及版本，`expected_version: 0` 创建，其余值须为当前范围版本。

字段缺失、全空组合或组合重复返回 `40000`，无登录返回 `40100`，无权限返回 `40300`，当前空间未登记范围时查询返回 `40400`，上游绑定不存在或任一预期版本不匹配返回 `40900`。过期、上游映射不可用或版本变更时，已保存记录仍可读，`effective_scopes` 为空数组。

## P2-03 行列条件

沿用 P2-01 的认证、工作空间、管理员资格和数据源治理检查。范围、来源和错误沿用公共响应信封。

| 路径 | 请求与响应 |
| --- | --- |
| `GET /api/v1/business-datasets/{key}/row-column-policy` | 返回策略及 `version`，包含固定的 `dataset_version`、来源版本、有效期和 `definition`。 |
| `PUT /api/v1/business-datasets/{key}/row-column-policy` | 参数为 `expected_version`、`dataset_version`、`authority`、`source_version`、`valid_from`、`valid_until` 和 `definition`，返回保存后的策略。 |
| `POST /api/v1/business-identities/{user}/datasets/{key}/row-column-plan` | 参数为 `scope_version`、`policy_version`、`columns`。返回数据集键、身份/数据集/组织范围/策略版本、获准列及保留的组织组合数量。 |

`definition` 包含 `schema`、`view`、`dimension_columns`（可映射 `department_key`、`warehouse_key`、`transport_org_key`）、`allowed_scopes`、`denied_scopes`、`allowed_columns` 和 `denied_columns`。组织范围沿用 P2-02 的完整组合，禁止清单可为空。标识符限 ASCII 字母、数字和下划线，首字符不能为数字，长度最多 63。请求列及各清单最多 100 条。计划摘要只用于校验。

参数或组合不合法返回 `40000`，无认证返回 `40100`，空间权限不足、策略拒绝或配置已过期返回 `40300`，策略查询无对应记录返回 `40400`，版本冲突或策略登记目标不支持时返回 `40900`。

## P2-04 字段分类与默认输出计划

沿用 P2-03 的工作空间与管理资格。以下为分类登记、计划校验及受控明文元数据接口。审批还须匹配数据集内专门登记的独立复核人。

| 方法和路径 | 请求与响应 |
| --- | --- |
| `GET /api/v1/business-datasets/{key}/output-policy` | 返回分类策略及 `version`、`dataset_version`、`row_column_policy_version`、来源版本、有效期、`fields` 和明文配置。 |
| `PUT /api/v1/business-datasets/{key}/output-policy` | 提交 `expected_version`、`dataset_version`、`row_column_policy_version`、`authority`、`source_version`、`valid_from`、`valid_until`、`fields`，可选 `record_key_column`、`record_version_column`、`reviewer_ids`。 |
| `POST /api/v1/business-identities/{user}/datasets/{key}/output-plan` | 提交 `scope_version`、`policy_version`、`output_policy_version`、`columns`。核查明文时附 `reveal_request_id`、`purpose`、`channel: interactive_check`，且 `user` 必须为当前申请人。普通计划使用默认脱敏。 |
| `POST /api/v1/business-datasets/{key}/reveal-requests` | 提交组织范围、行列策略、输出策略三个版本、`columns`、`purpose`、`records`（`{key, version}` 列表，最多 100 项）、`ttl_seconds`（1 至 86400）。返回申请元数据。 |
| `POST /api/v1/business-reveal-requests/{request}/approve` | 提交 `ttl_seconds`（1 至 28800）。指定复核人须与申请人不同，并以其当前有效的组织范围覆盖本次申请。返回审批元数据。 |
| `POST /api/v1/business-reveal-requests/{request}/revoke` | 申请人或对应审批人收回申请，返回收回后的元数据。 |

`fields` 是以字段名为键的映射，最多 200 项。每项含 `classification`（`internal`、`sensitive`、`high_sensitive` 或 `credential_secret`），可含 `legacy_label`、`legacy_protection`（`none`、`masked` 或 `hidden`）、`review_reference` 和 `depends_on`（本策略内明确登记的来源列）。旧 `confidential` 须提交逐字段复核引用。字段命名遵循 P2-03。所有行列允许列必须完成分类，派生关系禁止循环和降级。

参数、未知分类或不完整登记返回 `40000`；无认证 `40100`；权限不足、分类策略不可用或请求禁止字段返回 `40300`；查询未登记策略 `40400`；写入时上游未确认或预期版本冲突 `40900`。计划摘要不含目标数据或 SQL。

## P2-05 已发布报表隔离验收

`POST /api/v1/published-reports/{id}/policy-verification` 接收 `dataset_key`、`publication_no` 以及上述输出计划参数。先按当前会话验证发布受众及查看资格，再验证输出策略的 `report_id` 和 `publication_no` 绑定。响应包含列名、结果行和脱敏摘要，使用 `Cache-Control: no-store`。

输出策略可选同时登记 `report_id` 与 `publication_no` 正整数。首批验收适用于无参数、无独立数据契约的正式发布。批准的 SQL 须为 `SELECT "列1", "列2" FROM "模式"."视图"` 形式，并与行列策略的完整允许列清单顺序一致。请求实际投影仍受列清单和禁止规则约束。验收入口需 development、显式开关及元库隔离登记。入口未启用返回 `40400`，无认证返回 `40100`，范围或查看资格不足返回 `40300`，发布号、批次或访问状态变化返回 `40900`，审计不可用时返回 `50300`。

## 正式报表与仪表盘

以下为当前受管内容接口，独立于个人reports和旧dashboards。业务与账号边界、草稿/冻结审批/发布三个维度、参数及数据契约见[内容与交付契约](../架构设计/内容与交付契约.md)，不把阶段3新多节点策略当作已实现。

| 方法与相对路径 | 关键请求及响应 |
| --- | --- |
| POST `/managed-reports`；PUT `/managed-reports/{id}/draft` | 报表定义及request_id，修改含version；返回id/version |
| POST `/managed-reports/{id}/submit` | version、request_id；冻结内容并建立审批记录 |
| GET `/managed-reports/{id}/versions`、`/managed-reports/{id}/releases` | 分页版本/审批与追加发布事件，分别保留真实状态及版本号 |
| GET `/managed-reports/{id}/versions?review_id={reviewId}` | 按关联审批编号读取此报表唯一冻结版本，沿用登录和报表管理读取权限。请求可省略 `page` 或传入 `1`，返回原分页结构，`items` 一项且 `total=1`。编号非正整数或请求其他页返回 400。编号无对应版本、与报表不匹配或对象不可见时返回 404。 |
| POST `/report-reviews/{id}/validate` | account_id、user_ids、group_ids、params及request_id；返回result、validation_id、manifest_hash |
| POST `/report-reviews/{id}/decision` | decision、validation_id、comment、contract_confirmed、request_id；批准须本人15分钟内且当前账号/受众等基线一致的验证，不可自审 |
| POST `/managed-reports/{id}/releases` | expected_publication_no、review_id、kind及request_id；publish/rollback/unpublish按当前资格及依赖检查 |
| POST `/managed-reports/{id}/transfer` | version、owner_id、request_id；责任人移交不转赠凭据或旧签名 |
| GET/POST `/report-accounts`；PATCH `/report-accounts/{id}`；POST `/report-accounts/{id}/test` | 创建含name/definition_revision_id/username/password/request_id；维护按version轮换密码或启停并确认影响；返回裁剪账号摘要，测试返回connected/read_only |
| GET `/published-reports`、`/published-reports/{id}`、`/metric-catalog` | 仅当前获准展示信息，不含SQL、目标地址及执行账号配置 |
| POST `/published-reports/{id}/execute` | publication_no、request_id、params；禁止覆盖SQL、目标、账号和事务 |
| POST `/managed-dashboards`；PUT `/managed-dashboards/{id}/draft` | definition、request_id，修改含version；definition含tiles、user_ids/group_ids、quality及parameters |
| GET `/managed-dashboards`、`/managed-dashboards/{id}` | 仪表盘开发目录或单项；`reviewer_eligible` 表示该项当前生命周期内最新审阅的原审阅人资格，没有审阅时为 `false` |
| POST `/managed-dashboards/{id}/preflight` | version；返回当前依赖及受众覆盖预检，不能作为长期授权凭证 |
| GET `/managed-dashboards/{id}/versions`、`/dashboard-reviews/{id}/summary` | 当前编排版本、独立审批摘要及实际差异；版本列表的 `reviewer_eligible` 表示该冻结版本的原审阅人当前是否具备审阅资格 |
| GET `/published-dashboards`、`/published-dashboards/{id}` | 当前获准看板及逐卡ready/restricted/contract_changed说明 |
| GET `/access-requests` | 当前工作空间的申请与实际生效发布号。普通身份仅读取本人提交或本人为目标的申请，响应不返回报表编号。动作受控审阅人按 `report.review` 或 `dashboard.review` 读取空间内的申请与关联审批，即使兼有 `task.page`。兼容身份继续按原审批职责。未登录返回 401，缺少入口许可返回 403。 |
| POST `/managed-dashboards/{id}/refreshes` | mode、review_id、params；返回refresh_id及固定本轮清单 |
| POST `/dashboard-refreshes/{id}/tiles/{tile}/execute` | 原刷新及卡片；返回本轮绑定结果、execution_id和quality，不按客户端新目标重解释 |

报表定义的parameters固定名称、类型、必填、默认值及允许范围；执行params只填已批准参数，不能拼入SQL或改变目标。data_contract的业务语义、结果结构、单位、范围和freshness随冻结版本维护；普通数值/NULL及水位按批准定义返回。

带 `data_contract` 的报表预览、正式复核和正式取数将 PostgreSQL 原生有限 `DATE` 返回为 `YYYY-MM-DD` 字符串。SQL NULL 继续返回 JSON `null`，`TIMESTAMPTZ` 继续返回带时区时间戳。复核或正式查询结果违反获批日期契约时返回 HTTP 409。预览通过 `contract_check` 返回诊断。

仪表盘tiles绑定report_id、contract_hash、required、comparison_group及follow_published，固定历史依赖和仪表盘文件/周期交付尚未开放。访问申请、仅调整受众、发布/归档/恢复等动作沿现有专用接口和版本控制，完整载荷见[managed-report.ts](../../frontend/src/api/managed-report.ts)、[managed-dashboard.ts](../../frontend/src/api/managed-dashboard.ts)及[处理器](../../backend/internal/reportapi/)。不把通用action字符串视为允许任意操作的协议。

目录、单项和版本列表沿用工作空间认证与各自现有读取权限。`reviewer_eligible` 为只读布尔值，独立于历史审批状态。发布或回滚仍按服务端提交时的资格校验，原审阅人资格已失效时返回 HTTP 409。

<a id="dashboard-role-matrix-47-api"></a>

#47 仅在明确启用的矩阵空间生效。接口使用原认证、空间上下文和命令请求编号。空间角色读取反映当前空间实际生效的内置权限版本。

| 接口 | #47 请求、响应与错误 |
| --- | --- |
| POST `/managed-dashboards/{id}/preflight` 和 `/submit` | 使用原草稿版本和送审复核人字段。候选与提交按仪表盘复核资格、独立性及卡片范围核对。无有效候选说明原因。提交的候选无效或为本人返回 400。草稿、审核状态或数据依赖变化返回 409。重送生成独立冻结版本与复核记录。 |
| GET `/managed-dashboards/{id}/versions` | 保留原签署记录和 `reviewer_eligible`。启用空间返回 `business_signer_id`、`can_publish`、`can_rollback`、`can_reassign`、`requires_re_review`，供当前会话按版本显示动作。失权时保留历史签署和原正式发布。普通主管仅可读本人受指派的复核。 |
| POST `/dashboard-reviews/{id}/validate`、`/decision` 及 `/reassign` | 沿用原请求及响应。服务端重查当前复核资格与指派。本人审批、请求者不符合指派或治理权限返回 403。证据过期、轮次或状态发生变化返回 409。已批准未发布且资格失效时，原签署保留，重新送审创建新版本。 |
| POST `/managed-dashboards/{id}/releases` | `kind=publish` 沿原 `request_id`、`expected_publication_no`、`review_id`。只允许当前仍合格的本次实际业务签署人手动发布，发布人须独立于提交人。发布越权、自发或借正常发布权限调用回滚返回 403，失权、旧签署或基线变化返回 409。成功仍返回新发布号及命令回执。 |

未认证返回 401。缺请求所在工作空间或资源访问资格时仍按原接口返回 403 或 404。管理目录和单项可带 `dashboard_matrix_enabled`、`review_requires_re_review`。普通主管只读取本人受指派的管理对象，作者继续按本人维护资格读取。启用通过获准维护操作调用 `dbhub_iam_enable_dashboard_matrix(workspace_id)`，首次成功返回 `true`，同一空间重复调用返回 `false`。已启用矩阵且工作空间仍有效才可执行；普通运行身份无启用权限。

同一命令编号换操作或内容返回409，未知状态先核实原记录。报告/看板审批、源状态、账号代次、当前受众及发布号每次重查；普通查看与审批预览分别授权。

<a id="dashboard-lifecycle-48-api"></a>

#### 仪表盘冻结版本、待办与开发衔接（#48）

沿用登录、工作空间及现有对象授权。未登录返回 401，缺少入口动作返回 403。审批与内容编号用于定位，读取及办理按当前资格核对。

| 接口 | 请求、响应和错误 |
| --- | --- |
| GET `/managed-dashboards/{id}/versions?review_id={reviewId}` | 可选正整数 `review_id`。省略沿用版本列表。提供时按空间、仪表盘和审批唯一过滤，不受通用列表窗口限制。成功时 `items` 仅含该审批的冻结版本，无效参数 400，审批不存在、对象不匹配或不具备该版本读权限 404。 |
| GET `/managed-dashboards`、`/managed-dashboards/{id}` | `active_version`、`review_version` 分别表示当前生效发布及最新可读复核对应的内容版本，无记录时为 0。发布序号、草稿修订号及内容版本分别展示。正式配置按原 `/published-dashboards/{id}` 权限读取并核对发布号。复核差异沿用 `/dashboard-reviews/{id}/summary`。 |
| GET `/inbox/todos` | 可选 `kind` 取 `dashboard_review` 或 `dashboard_publish`，在分页、计数之前筛选。省略兼容全集，无效过滤值 400。仪表盘事项的 `id` 为审批编号，`resource_id` 为仪表盘编号，并可带 `content_name`、`content_version`。 |
| 仪表盘待发布事项 | #47 已启用且当前实际业务签署人具备该审批的手动发布资格时返回 `dashboard_publish`。排除不满足独立数据签署、失权及已发布的版本。实际发布使用原命令及当前权限、依赖与并发检查。 |
| 草稿及卡片候选 | 复用正式报表保存、预览和发布接口。添加卡片从既有已发布报表接口及获批数据契约筛选。保存回执供同一设计上下文显示草稿状态，草稿编号不影响服务端卡片准入。 |

阶段 3 明确登记的隔离正式报表复用原送审、预览和发布接口。业务复核后若仍需数据复核，审批保持待审。报表版本读回可带 `approval_stage`。已认证且具备当前数据集授权与数据覆盖的数据复核人可使用 GET `/report-reviews/{id}/data-review` 读取冻结定义和待复核摘要。POST `/report-reviews/{id}/data-review` 接收 `request_id`、`manifest_sha256`、`decision` 与 `comment`，批准后才可进入正常发布。缺资格返回 403，签名不足、预览过期或基线变化返回 409。未登记的审批对象沿用原接口。

受管数据集的 `row-column-policy.definition.parameter_bindings` 可登记 `column`、`operator` 和 `domain`。`domain` 使用报表参数定义，包括名称、类型、必填、枚举或数值边界。日期边界用 `date_min`、`date_max`，格式为 `YYYY-MM-DD`。支持 `eq`，以及同列成对的 `gte` 和 `lte`。固定查询在投影后按报表参数顺序串接比较条件，列及参数类型使用批准的比较类型。调用继续使用原 `params`，域外值及倒置区间返回 400，冻结定义与数据集登记无法匹配返回 409。本机隔离验收的登记示例见相应技术证据。

阶段 3 隔离仪表盘沿用原送审、逐卡预览、业务审批和发布接口。高风险业务复核后返回 `approval_stage=data_review`。已认证且具备全部引用数据集授权及覆盖的数据复核人可用 GET `/dashboard-reviews/{id}/data-review` 读取冻结定义与复核指纹，再用 POST 同路径提交 `request_id`、`manifest_sha256`、`decision` 及 `comment`。缺资格返回 403，审批、预览或基线过期及缺签发布返回 409。原兼容仪表盘沿用已有协议。

阶段 3 隔离对象通过 POST `/managed-reports/{id}/availability` 和 `/managed-dashboards/{id}/availability` 办理下线与应急。请求须携带认证会话、空间上下文、UUID `request_id`、正整数 `expected_publication_no`、动作 `action` 和非空 `reason`（最多 1000 字符）。

| action | 当前资格与请求补充 | 响应 |
| --- | --- | --- |
| `request_offline` | 对象负责人或业务复核人登记原因 | `id` 为申请号，`version` 为基准发布号，`dependency_count` 为受影响的对象数 |
| `approve_offline` | 有当前对象及下线资格的独立业务复核人传入 `approval_id` 及原申请原因，并确认同一依赖基线 | `id` 为下线事件号，`version` 为新发布号 |
| `emergency_stop` | 具备当前对象应急停止授权的人员登记原因 | `id` 为停止事件号，`version` 为新发布号 |
| `emergency_restore` | 具备当前对象应急恢复授权并覆盖当前数据边界，继续执行显式发布限制。恢复人与停止人不同，恢复目标由服务端按停止事件确定 | `id` 为恢复事件号，`version` 为新发布号 |

参数无效返回 400，未认证返回 401，缺资格返回 403，对象不存在或未纳入隔离方案返回 404，基准变化、旧发布不可恢复或无法证明当前约束时返回 409。已纳管对象通过上述动作下线及应急恢复，通过既有正常发布流程重新送审上线。

托管期间及接任后保留原发布时，已获当前访问资格的请求仍须符合该发布的原审批快照。正式报表查询、仪表盘卡片执行、导出生成及文件领取、订阅快照领取遇范围扩大或原审批边界无法核验时返回 409，响应不包含结果或文件。订阅任务遇同类冲突时暂停该订阅并停止当期交付。现有认证、工作空间和请求字段继续适用。

## 报表导出、个人订阅与告警

阶段 2 本地隔离验证复用正式报表发布、执行、导出及个人订阅的原认证和请求字段，由服务端登记报表与数据集。绑定报表的发布遇未知分类或策略不一致返回 409，未生效的隔离配置或缺少当前数据资格返回 403。结果执行和已有快照领取按当前版本复核，脱敏字段返回空值。通知负载仅包含提示及需要现有鉴权的资源引用。

P4-01 的隔离登记报表继续使用原认证、空间及导出申请、预览、审批请求字段。申请仅生成供独立复核的预览。审批成功返回审批状态和可选的 `generation_status`，其值为 `awaiting_approval`、`queued`、`running`、`succeeded`、`failed`、`interrupted` 或 `rejected`，可从原导出详情读回。重复审批版本和过期或已变化的审批边界返回 409，权限不足返回 403，行数或字节超限返回 413。新模式的本人文件领取待 #27，原下载接口对新记录返回 403，原模式及存量记录复用原下载规则。

P4-02 使用原认证及空间上下文。新个人计划保存后待本人确认，列表返回 `review_required` 和可选 `review_until`。POST `/report-subscriptions/{id}/confirm` 接收 UUID `request_id` 和当前 `version`，返回计划编号和新版本。团队 POST `/report-team-subscriptions` 接收 `request_id`、`definition`、`user_ids`、`group_ids` 和可选的续期或重提 `previous_id`。GET `/report-team-subscriptions/{id}` 返回申请及冻结名单。独立复核人通过 GET 同对象的 `/review` 获取 `basis_sha256`，POST 同路径携带 `request_id`、`version`、`basis_sha256`、`decision` 和 `comment` 作出决定。错误输入返回 400，权限不足返回 403，不存在或不属当前空间返回 404，版本、发布或复核边界变化返回 409。

P4-03 继续使用当前认证和空间。POST `/report-exports/{id}/credential` 由原申请人申请一次性凭证，返回 `credential` 和 `expires_at`。POST `/report-exports/{id}/redeem` 接收 `credential` 并返回已生成的文件字节。团队 POST `/report-team-subscriptions/{id}/recipient-access` 接收 `request_id`、`recipient_id`、收件资格的预期 `version`、`action` 和 `reason`。团队 POST `/report-team-subscriptions/{id}/periods` 接收 `request_id` 和获批计划 `version`，由当前负责人显式触发一期。GET 同计划的 `/periods/{period}` 向负责人返回各收件人的状态。收件人从 GET `/report-team-deliveries` 识别本人期次，以 GET `/report-team-deliveries/{id}` 领取本人快照。无当前资格、旧凭证或已撤权限拒绝，已变化的复核边界返回 409，其他主体的结果返回 404。

GET `/report-exports/{id}` 对当前能领取已生成文件的原申请人返回 `file_expires_at`。该字段只表示读回时的资格，申请凭证和兑换仍分别校验。审批人和兼容记录不通过此字段启用新文件领取。

P4-04 的新交付通知在本人站内列表返回 `link_expires_at`。GET `/inbox/delivery/{id}` 以当前登录和原空间打开本人通知，返回 `resource_type`、`resource_id` 与可选 `delivery_id`，由对应领取接口执行当前权限及期限检查。团队报表通知以 `delivery_id` 的 UUID 定位本人结果。过期、不存在或属其他成员的通知引用返回 404。通知自身仍可按原留存规则查看状态。个人告警事件可返回 `observed_value`，仅在存在原发生值证据且当前资格覆盖时提供。团队自动期次沿用逐人期次领取接口。

P4-05 的仪表盘交付接口使用当前登录、原空间及 `dashboard.view`，后台另查隔离登记、当前发布和各操作的独立资格。写入计划、复核、收件授权及告警处置使用 UUID `request_id`。

| 方法与路径 | 请求及返回 |
| --- | --- |
| POST `/published-dashboards/{id}/delivery-plans` | 提交 `kind`（`export`、`personal` 或 `team`）、`publication_no`、`params` 和可选 `tile_key`，返回计划 `id`、`version`。导出另带 `format`（`csv`、`json` 或 `xlsx`）及 `reason`。个人订阅带 `confirmed=true`、`interval_minutes` 和可选 `alert_tile`、`alert_rule`。团队订阅带 `interval_minutes`、`user_ids` 或 `group_ids`。订阅重提可带 `previous_id`。 |
| GET、POST `/dashboard-delivery-plans/{id}/review` | 读取受限复核依据，包括冻结参数、数据集、卡片键和收件名单。POST 提交 `version`、`basis_sha256` 及 `decision`，返回计划及新版本。 |
| POST `/dashboard-delivery-plans/{id}/recipient-access` | 独立授权人提交 `recipient_id`、预期 `version`、`action`（`grant` 或 `revoke`）及 `reason`，返回收件授权新版本。 |
| GET `/dashboard-delivery-plans/{id}/runs`、`/dashboard-deliveries` | 前者向计划负责人返回各期次的送达状态，后者返回本人交付记录及 `plan_id`、`dashboard_id` 和 `kind`。两个接口均不返回业务载荷。 |
| GET `/dashboard-deliveries/{id}` | 按当前资格返回本人交付的 `kind`、`sha256`、`expires_at`、`dashboard_id`、`publication_no` 和可选的单卡 `tile_key`。订阅返回原期次的 `snapshot`，导出返回文件 `format`。整板文件是逐卡分文件的 ZIP，单卡保留所选格式。 |
| POST `/dashboard-deliveries/{id}/credential`、`/dashboard-deliveries/{id}/redeem` | 前者返回本人会话的一次凭证及有效期，后者接收 `credential`，返回原文件字节及 `X-Report-File-SHA256`。 |
| GET `/dashboard-delivery-plans/{id}/alerts`、POST `/dashboard-delivery-plans/{id}/alerts/{event}/disposition` | 读取个人指标卡事件及经授权的原值，或提交 `note` 保存处置。 |

参数无效返回 400，越权返回 403，其他主体的记录及过期领取返回 404，发布、批准依据或数据边界变化返回 409。元信息、快照、出件及文件兑换分别校验当前资格。

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
| GET `/source-tasks`、`/source-tasks/{id}` | 本人任务、原空间、状态、阶段及裁剪结果。列表可传正整数 `connection_id`，先按连接筛选再计算 `total` 和分页 |
| POST `/source-tasks`；POST `/source-tasks/{id}/retry` | 创建含connection_id、kind=check/collect、request_id；重试含新request_id并保留旧任务，不自动重放 |
| POST `/source-tasks/{id}/cancel` | 排队取消立即处理，运行中尽力取消，不代表已回滚已提交快照 |
| GET/PUT `/source-schedules` | 保存connection_id、kind、interval_minutes、enabled、version；读取本人计划可传正整数 `connection_id` 缩小结果范围 |
| GET `/source-check-history` | 已保存连接的直接测试及后台连接检查回执。`result.type` 为检查时的连接类型，JDBC 回执可含 `result.compatibility.product`。旧记录可能缺少类型字段。检查结果不能显示为持续在线 |
| GET `/inbox`、`/inbox/todos`；POST `/inbox/read` | 本人通知及实时待办，单条或列表高水位已读；已读不等于业务处理完成 |
| GET `/business-operations/templates`、`/business-operations/accounts` | 当前可办理模板或有权维护的账号摘要 |
| POST `/business-operations/previews` | template_id、version、target_key、amount；返回原目标版本、前后摘要及预览编号 |
| POST `/business-operations/requests` | preview_id、reason、request_id；返回申请id/version |
| POST `/business-operations/requests/{id}/validate`、`/business-operations/requests/{id}/decision` | 当前version、核验结果及独立决定；批准含validation_id、confirmed和request_id |
| POST `/business-operations/requests/{id}/execute`、`/business-operations/requests/{id}/reconcile` | 执行含version/confirmed，核实沿原operation_id；返回原申请状态及裁剪回执，unknown不得自动重跑 |

数据源任务提交返回 HTTP 202 和本次任务标识，排队、执行及终态由 `status` 与 `phase` 表示。兼容字段 `progress` 为阶段刻度，当前检查与采集接口没有全任务的计量总量。采集结果以成功后的 `result` 为准。

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
| GET `/iam/custody-items`、`/iam/custody-nominations` | 同空间治理读者查询逐项资产，候选人查询本人提名。只返回对象、版本、依赖和交接状态。 |
| POST `/iam/custody-items/{item}/handover` | 已认证且在登记的隔离环境中。请求含 `request_id`、`version`、`action`、`reason`。治理人 `nominate` 时提供 `candidate_id`，候选人 `accept` 或 `reject`，由另一治理复核人 `approve`。提名和批准须携带委派号和版本。返回对象号、交接状态及新版本。无效请求 400，缺资格 403，未登记或不可见 404，基线变化 409。 |
| POST `/iam/custody-rejoins` | 已认证且在登记的隔离环境中，由两位治理人分别提交 `propose` 与 `approve`。请求含 `request_id`、`action`、`target_id`、`reason`、`delegation_id` 与 `delegation_version`。批准附 `proposal_id`。返回提案状态，批准后返回新的原空间成员编号。随后用同一平台账号重新登录。400、403、404、409 含义同上。 |
| POST `/iam/proposals/preview`、`/iam/proposals` | 预览和提交角色定义、角色分配、组增加、委派、限期解除禁止或隔离包启用；均不立即扩权 |
| POST `/iam/proposals/review` | 审批精确变更；申请人与全部受益人回避，同时核实当前复核资格、委派范围及原基线 |
| POST `/iam/restrictions` | 按 `kind` 检查精确许可：`member.freeze` 需要 `space.members.exit`，`role.disable` 需要 `space.roles.manage`，`assignment.revoke` 和 `delegation.revoke` 需要 `space.assign.manage`，`action.revoke` 需要 `space.resources.revoke`。未知动作拒绝。服务端继续核对当前治理委派、目标和基线，将限制与递增代次原子持久化。 |
| POST `/iam/restrictions` 的 `exit_mode` | `member.freeze` 可在登记的隔离环境显式传入 `planned`。若冻结后没有仍有效的治理委派接续，返回 409。缺省沿现有紧急冻结处理。 |
| GET `/iam/restrictions` | 查询当前治理委派目标与许可范围内的最新操作禁止、限期解除和审批历史；需要`space.assign.view`，不提供任意成员的禁止目录 |
| GET/PUT `/iam/menus` | 11个稳定目录的完整显示配置；只支持名称、分类、顺序、显示和推荐首页，不接受任意路由或动作 |
| PUT `/iam/home` | 保存本人在当前空间可见且有权访问的菜单首页 |
| GET `/iam/resources`、`/iam/policy` | 当前原资源、原业务审批策略的只读说明；新增数据范围、多节点业务审批策略在后续阶段实施 |
| GET `/iam/routes`、`/iam/migration/bridge` | 实际展开的有限接口规则与当前运行版本核对，不作为授权凭证 |
| POST `/iam/migration/shadow` | 元数据只读对照，不取目标数据库业务数据、不导出、不送达消息 |
| POST `/iam/bundles`、`/iam/bundles/rollback` | 准备配置包或执行兼容回退；仅服务器与数据库双重登记的隔离环境可使用；启用还需独立审批和当前运行桥核对 |

`GET /iam/effective` 可为已登记隔离部署中采用旧权限方案的当前主体返回 `isolated_migration`，字段 `prepare`、`review`、`rollback` 为布尔值。仅在现有治理能力与至少一项当前委派均有效时返回该对象。前端用它展示对应迁移入口，提交、复核和回退仍按原接口检查委派上限、回避、配置基线和运行证明。仍使用本人会话和工作空间上下文；401、403 及原写入接口的 409 语义保持现有约定。

治理写入使用UUID `request_id`、明确的`delegation_id`与`delegation_version`、当前`baseline={config_version,authorization_epoch}`以及精确目标版本。相同请求与相同内容返回原回执；同编号换内容或基线变化返回冲突。有限委派同时约束治理动作、可授许可、目标成员、数据源及期限；本人可执行不等于可授予。

`GET /iam/assignments` 继续要求登录及原工作空间的 `space.assign.view` 许可。已有分配来自治理提案时返回 `proposal_id`（UUID），供成员详情按实际申请编号关联已落地分配。缺少对应提案的历史分配返回空值。认证、空间边界、查询范围以及 401、403 响应沿用原契约。

撤权持久化成功与在途执行结束是两个状态：`state=applied`表示限制已保存，新授权检查使用新代次；`execution_fence=confirmed`才表示已核实兼容执行边界。无法立即取得执行屏障或运行版本证明时返回HTTP 202、`code=0`和`execution_fence=pending`，不得据此宣称在途写事务已撤回。原请求编号及已产生的业务回执保持，不自动重发写操作。普通HTTP成功回执与此202都不能代替业务最终结果。

以下接口仅要求当前身份，不依赖仍有效的空间成员资格：

- GET `/api/v1/iam-command-status`及`/{workspace}/{request}`：只返回本人提交的收紧回执；冻结或退出原空间后仍可核实执行边界，不返回他人目录或凭据。
- GET `/api/v1/iam-recovery`、POST `/api/v1/iam-recovery/requests`及`/api/v1/iam-recovery/verifications`：原空间治理恢复。两名不同预登记身份各自实际核验；MFA已启用时仍需MFA。通过后还需重新登录，最多15分钟，仅限治理，不能恢复业务查看、个人连接、原角色来源或旧订阅。

角色名称自由配置；多来源合并不会提高原数据身份。未知主体保持完整旧兼容路径，TARGET拒绝后不会回退旧admin。本人安全、通知及获准平台入口独立于11个空间菜单；隐藏菜单不撤权，实际撤许可后URL与API均拒绝。新菜单加载失败或原空间失效时，不自动进入其他空间。

矩阵范围内，`PUT /iam/menus` 在原配置、请求号及版本校验上支持 `configuration.role_visibility`，以角色 UUID 和菜单 ID 为键，单元值为 `show/hide`，缺少单元继承。省略矩阵字段保留原矩阵，空对象清除覆盖。服务端拒绝未知范围、不可配置单元和未获超级管理员资格的修改，沿用原错误信封与版本 409。有效权限响应提供 `matrix_enabled`、`is_super_admin`、`reserved_actions` 及有效 `visible_menu_ids`，后者用于矩阵范围内的导航及首页。原始矩阵仅向具备同一空间管理许可的超级管理员提供。菜单保存不增加业务许可。角色动作仍走现有审批、委派及资源检查，超级管理员专属限制在执行时仍有效。

配置回退不删除增量表、撤权账和审计，不恢复冻结、到期或已撤销来源；原业务签名、发布号、7天文件期限和结果未知回执持续保留。真实启用须按路线图另行明确授权；当前仅有隔离实现与技术证据，产品和独立验收仍分开记录。

### 限期解除操作禁止

使用上述治理提案接口，`patch.kind=action_release`，明确填写`subject_id`（成员）、`action_id`、`revocation_sequence`（当前最新禁止记录）、`reason`和`valid_until`。使用原有`space.assign.manage`及有限委派中的`assignment.propose/assignment.review`；不新增许可编号、通配目标或数据资源授权。

提交与批准均核对当前空间基线、最新禁止记录、受益成员、许可启用状态和双方委派的目标、许可与期限上限。申请人及受益人不能复核自己的解除；批准、记录及审计原子提交，相同请求只返回原结果。数据库同时检查解除记录与已生效独立提案的精确对应，解除及原禁止历史均保留。

解除只在获批期限内覆盖该记录及更早的同成员同操作禁止；到期后禁止自动恢复，后来新增的禁止立即优先。解除不重建已撤销的角色来源、不恢复冻结成员、不提高原数据身份，也不解除其他工作空间的限制。到期后再次解除必须提交新的独立提案。页面位于当前空间「角色分配 → 禁止操作与解除审批」，待复核时明确显示仍禁止，审核在「治理审批」完成。
