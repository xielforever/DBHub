# 接口规范 (API Specification)

本轮云端补齐的新增接口、错误语义和验收约束见 [云端后端补齐与验收契约](../开发指南/云端后端补齐验收.md)。与历史“占位/规划”描述冲突时，以该契约和已通过的集成验证为准。

本规范定义了 DBHub 后端接口的交互标准。

## 1. 认证模块 (Auth)

### 1.1 用户登录
**POST** `/api/v1/auth/login`（无需认证）

**Request:**
```json
{
  "username": "admin",
  "password": "your_secure_password"
}
```

**Response:**
```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "token_type": "Bearer",
    "access_token": "eyJhbGciOiJIUzI1Ni...",
    "refresh_token": "eyJhbGciOiJIUzI1Ni...",
    "expires_at": "2026-09-14T14:39:20Z",
    "user": {
      "id": 1,
      "username": "admin",
      "role": "admin"
    }
  }
}
```

> 密码在服务端使用 PBKDF2-HMAC-SHA256（210,000 次迭代）校验；
> Access Token 有效期 15 分钟，Refresh Token 有效期 7 天。
> 用户不存在与密码错误均返回相同提示（`40100 用户名或密码错误`），防止账号枚举。

**Curl 示例:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}'
```

### 1.2 刷新令牌
**POST** `/api/v1/auth/refresh`（无需认证，请求体携带 Refresh Token）

**Request:**
```json
{
  "refresh_token": "eyJhbGciOiJIUzI1Ni..."
}
```

响应结构与登录接口一致，返回全新的令牌对。服务端会重新加载用户状态，
被禁用的账号即使持有有效 Refresh Token 也无法换取新令牌。

### 1.3 获取当前用户
**GET** `/api/v1/auth/me`

**Request Header:**
```
Authorization: Bearer <ACCESS_TOKEN>
```

**Response:**
```json
{
  "code": 0,
  "message": "ok",
  "data": { "id": 1, "username": "admin", "role": "admin" }
}
```

### 1.4 令牌携带的三种通道

除登录/刷新/健康检查外，所有接口均需携带 Access Token，服务端按以下
**优先级顺序**依次识别（三通道等价，便于浏览器、CLI 与嵌入场景）：

1. `Authorization: Bearer <ACCESS_TOKEN>`（标准，推荐 API/CLI 使用）；
2. `X-Access-Token: <ACCESS_TOKEN>` 请求头（适用于无法设置
   Authorization 的网关/下载链接场景）；
3. Cookie `dbhub_access_token=<ACCESS_TOKEN>`（浏览器同源场景兜底）。

Access Token 过期（401）时，响应拦截器应使用 Refresh Token 静默换取新
令牌对并自动重放原请求一次；Refresh Token 也失效时再跳转登录页。
浏览器在登录后应同时写入 localStorage（供前两通道）与 Cookie（通道 3）。

### 1.5 健康检查
**GET** `/api/health`（无需认证，供容器探针使用）

```bash
curl http://localhost:8080/api/health
```

### 1.6 统一错误码

| HTTP | code   | 含义         |
|------|--------|--------------|
| 400  | 40000  | 请求参数错误 |
| 401  | 40100  | 未认证/令牌失效 |
| 403  | 40300  | 无权限       |
| 404  | 40400  | 资源不存在   |
| 409  | 40900  | 资源冲突     |
| 500  | 50000  | 服务器内部错误（细节仅写日志，不返回前端） |

---

## 2. 早期草案（已废弃，保留仅为变更追溯）

> 以下接口路径均为设计草案，**从未实现，禁止对接**：
> `POST /api/v1/connections/test`（现行为 POST `/api/v1/connections/test` 与
> `POST /api/v1/connections/{id}/test`，见后文「数据源管理」）、
> `GET /api/v1/db/:connection_id/databases`（现行为
> `GET /api/v1/metadata/databases?connection_id=`）、
> `POST /api/v1/db/:connection_id/query`（现行为
> `POST /api/v1/query/execute`）。SQL 参数化安全要求仍然有效：
> 平台侧所有动态值通过数据库驱动的参数绑定/simple 协议客户端转义传递，
> 禁止拼接用户输入。请以后续同名章节为准。

---

## 3. WebSocket 实时接口（规划中，当前版本未实现）

用于长时间运行的任务（如大表迁移、日志流）或实时通知。

**Endpoint**: `/ws`

**Events:**
- `server:health`: 推送服务器负载
- `task:progress`: 推送异步任务进度 (taskId, percentage, message)

---

## 4. 数据源管理 (Connections)

> 除标注外均需 Access Token（三通道任一）。创建连接限 `admin`、`developer`；修改和删除限管理员或连接的 developer 创建者，`readonly` 不可执行。所有已登录角色可测试已保存配置，编辑草稿的测试权限见 4.5。
> **数据源为团队共享资源**：任何登录用户均可查看完整连接列表，并可对全部数据源执行元数据浏览、表预览与（只读）SQL；列表与使用不再按创建人过滤。`user_id` 仅记录创建者用于审计，个人可见性维度预留给后续「个人连接」类型。写操作（增删改数据源、隧道）仍在路由层按角色鉴权。

### 4.1 连接列表
**GET** `/api/v1/connections?type=postgres&keyword=prod`

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

### 4.2 创建连接
**POST** `/api/v1/connections`
```json
{
  "name": "订单库", "type": "mysql", "host": "10.0.0.11", "port": 3306,
  "database": "orders", "username": "app", "password": "s3cret",
  "ssh_tunnel_id": null, "ssl_mode": "require",
  "connection_timeout": 10, "color_label": "", "environment": "prod"
}
```
`type` 仅允许 `mysql | postgres | redis`；口令经 AES-256-GCM 加密后落库。
`environment` 可选，仅允许 `dev | test | prod`，缺省回落 `dev`（资产树环境徽标、M6 行内编辑 prod 二次确认的依据）。成功返回 `201` 与连接详情。

### 4.3 修改连接
**PUT** `/api/v1/connections/{id}`，请求体同创建。`password` 留空表示保留原口令不修改。

### 4.4 删除连接
**DELETE** `/api/v1/connections/{id}`，级联删除其查询历史。

### 4.5 测试连接
- **POST** `/api/v1/connections/test`：请求体同创建（用于保存前验证，口令不落库）
- **POST** `/api/v1/connections/{id}/test`：空请求体使用已保存配置测试；携带完整连接配置时测试编辑草稿

草稿测试仅限管理员或该连接的 developer 创建者。口令留空复用已保存口令；修改数据库类型、主机、端口、账号、TLS 或代理/SSH 隧道后，必须重新输入口令。草稿测试不修改配置或已保存配置的检查记录。

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
失败返回 HTTP 400 / `40000`，消息为清洗后的驱动错误（不含口令片段），`data` 包含 `ok=false`、`error`、耗时与检查时间。保存配置的成功/失败检查都会持久化最近一条，列表/详情以可选 `last_check` 返回。`recorded=false` 表示草稿测试，或该结果因配置变更/较新检查而未记录；配置修改后旧检查失效，删除连接时级联删除记录。详情见[检查与采集可靠性](../开发指南/数据源检查与采集可靠性.md)。

`version` 保留原有响应字段；`compatibility.version` 为规范化版本。`ok` 表示连接成功，兼容结论另见 `status`：`verified` 为实测补丁；`branch_verified` 为同分支其他补丁；`unverified` 为尚未验证的版本、兼容产品或 Redis 部署模式；`unknown` 为版本无法可靠识别。Redis 账号可以连接但无权读取 INFO 时返回成功与 `unknown`，不伪造验证结论。Redis 的 `compatibility.mode` 表示服务端报告的运行模式。具体边界见[数据源版本兼容](../开发指南/数据源版本兼容.md)。

### 4.6 版本验证范围

**GET** `/api/v1/connections/compatibility`，所有已登录角色可读，未登录返回 `401`。本接口只读取内嵌验证记录，不连接目标数据库。

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

## 5. SSH 隧道 (SSH Tunnels) — 已废弃，保留兼容

> **废弃说明**：M4 前为跳板机场景设计，仅支持 SSH 私钥/口令。M5 起演进为 **代理管理（Proxy）**，支持 `http/https/socks5/db_proxy/custom`，更通用。`sys_ssh_tunnels` 表保留兼容，`sys_proxies` 为新表，`connections.proxy_id` 已占位。前端已新增 `/proxies` 占位页，`/connections` 隧道选择器将在 M5 切换为代理选择器。

### 5.1 列表 / 创建 / 修改 / 删除（兼容）
- **GET** `/api/v1/ssh-tunnels` → `{ "items": [...], "total": n }`
- **POST** `/api/v1/ssh-tunnels`（写角色）
- **PUT** `/api/v1/ssh-tunnels/{id}`（写角色；凭据字段留空不覆盖）
- **DELETE** `/api/v1/ssh-tunnels/{id}`（写角色；被连接引用时自动置空）

```json
{
  "name": "生产跳板机", "host": "10.0.0.9", "port": 22,
  "username": "ops", "auth_type": "private_key",
  "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
  "passphrase": "可选", "password": "auth_type=password 时使用"
}
```
> 列表仅返回 `has_private_key / has_passphrase / has_password` 标志；私钥与口令 AES-256-GCM 加密落库。

### 5.2 测试跳板连通（兼容）
**POST** `/api/v1/ssh-tunnels/test`，请求体同上（支持保存前验证），成功返回 `{ "ok": true }`。

## 5.1 代理管理 (Proxies，占位 M4，M5 正式实现)

> **占位**：替代 SSH 隧道，统一抽象代理层。读：登录可用；写：`admin/developer`；`readonly` 403。M4 仅保存配置，拨号逻辑 M5 实现。

- **GET** `/api/v1/proxies` → `{ items: [{ id, name, type, host, port, username, has_password, description, status, created_at, updated_at }], total, note: "占位" }`
  - `type ∈ http|https|socks5|db_proxy|custom`
- **POST** `/api/v1/proxies` `{ name(必填), type, host(必填), port(必填), username?, password?, description? }` → 代理详情（密码加密，`has_password` 布尔）
- **PUT** `/api/v1/proxies/{id}` 部分更新
- **DELETE** `/api/v1/proxies/{id}` → `{ id }`，自动清理 `connections.proxy_id`
- **POST** `/api/v1/proxies/test` `{ type, host, port, username?, password? }` → `{ ok: true, note: "占位，M5 实现" }`

前端占位页 `/proxies`：类型卡片（HTTP/HTTPS/SOCKS5/DB Proxy）+ 列表 + 新建/编辑 Dialog + 测试占位，暗色玻璃，`Waypoints` 图标。

---

## 6. SQL 工作台 (Query & Metadata)

### 6.1 执行 SQL
**POST** `/api/v1/query/execute`
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

**GET** `/api/v1/query/executions/{request_id}`：查询本人执行回执，其他用户（含管理员）不能跨用户读取。示例 `data`：

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

取消、超时、断网后只查询回执，不自动重新提交 SQL。超过 5 分钟未完成的排队/执行记录展示为 `unknown`，保持编号占用。回执不保存查询行，确认查询完成后只显示完成摘要。完整一致性与部署边界见 [执行可靠性](../开发指南/SQL工作台执行可靠性.md)。

### 6.2 对象浏览（均为 GET）
| 接口 | 说明 |
| --- | --- |
| `/metadata/databases?connection_id=` | 逻辑库列表（自动过滤系统库） |
| `/metadata/schemas?connection_id=&database=` | PostgreSQL 模式列表 |
| `/metadata/tables?connection_id=&database=&schema=` | 表与视图（`type=table/view`） |
| `/metadata/columns?connection_id=&database=&schema=&table=` | 列结构（类型/可空/主键/默认值/序号） |

### 6.3 表数据分页预览
**GET** `/api/v1/data/preview?connection_id=1&database=orders&schema=public&table=users&page=1&page_size=50`
```json
{ "columns": [...], "rows": [[...]], "total": 1320, "page": 1, "page_size": 50, "has_more": true }
```
> 表名/模式名经服务端标识符转义（PG 双引号、MySQL 反引号），杜绝表名注入。

### 6.4 Redis 浏览（GET）
| 接口 | 说明 |
| --- | --- |
| `/redis/overview?connection_id=` | 版本/模式/运行天数/客户端数/内存/键空间统计 |
| `/redis/keys?connection_id=&pattern=user:*&limit=200` | SCAN 游标遍历键（默认上限 500），含类型与 TTL |
| `/redis/value?connection_id=&key=k` | 按类型预览值（string/list/hash/set/zset，大元素截断 100 项） |

### 6.5 查询历史
- **GET** `/api/v1/query/history?connection_id=&status=success|failed&page=1&page_size=20`
  → `{ items, total, page, page_size }`，普通用户仅本人记录，admin 全部；
- **DELETE** `/api/v1/query/history/{id}`：删除单条（仅本人/admin）；
- **DELETE** `/api/v1/query/history`：清空本人历史（admin 清空全部）。

---

## 7. 用户与角色 RBAC（仅 admin）

> 所有接口需 Bearer Token 且角色为 `admin`，否则返回 `40300`。

| 方法与路径 | 说明 |
| --- | --- |
| GET `/api/v1/roles` | 内置角色列表（admin/developer/readonly） |
| GET `/api/v1/users?keyword=&page=&page_size=` | 用户分页列表（含角色、状态、最近登录） |
| POST `/api/v1/users` | 创建用户 `{username(>=3), password(>=8), role, email?}`，重名返回 `40900` |
| PUT `/api/v1/users/{id}` | 修改角色/邮箱（不能降低自己角色；须保留至少一个启用管理员） |
| PATCH `/api/v1/users/{id}/status` | 启用/停用 `{is_active}`（不能停用自己） |
| POST `/api/v1/users/{id}/reset-password` | 管理员重置密码 `{new_password}` |
| DELETE `/api/v1/users/{id}` | 删除用户（不能删除自己；级联清理其连接/历史） |

### 7.1 修改自己的密码（任意登录用户）
**POST** `/api/v1/auth/change-password`
```json
{ "old_password": "...", "new_password": "至少 8 位" }
```
原密码错误返回 `40000`；新密码不得与原密码相同。

### 7.2 角色权限矩阵
| 能力 | admin | developer | readonly |
| --- | --- | --- | --- |
| 仪表盘/查看数据源 | ✅ | ✅ | ✅ |
| 数据源/隧道增删改、测试连接 | ✅ | ✅ | ❌（403） |
| 浏览共享数据源（库/模式/表/预览） | ✅ | ✅ | ✅ |
| 执行 SELECT / SHOW / EXPLAIN 等查询 | ✅ | ✅ | ✅（引擎层关键字校验） |
| 执行 INSERT/UPDATE/DDL 等写操作 | ✅ | ✅ | ❌（400 拒绝：只读角色禁止执行非查询语句） |
| 用户与角色管理、审计日志 | ✅ | ❌ | ❌ |
| 修改自己的密码 | ✅ | ✅ | ✅ |

---

## 8. 审计日志（仅 admin）

**GET** `/api/v1/audit-logs?days=7&username=&action=&resource_type=&status=success|failed&page=&page_size=`

关键写操作（登录、改密、连接/隧道/用户增删改、测试连接、SQL 执行、历史清理）
由审计中间件自动落库，记录操作者、来源 IP、User-Agent、结果状态、耗时与 trace_id；
请求体不落库以防泄密。返回 `{ items, total, page, page_size }`。
`action` 取值：`LOGIN / CHANGE_PASSWORD / CREATE / UPDATE / DELETE / TEST / QUERY`。

## 9. 仪表盘指标（Metrics）

### 9.1 仪表盘汇总

**GET** `/api/v1/metrics/overview?days=14`

登录用户均可访问；`days` 取 1~90，缺省/越界均回落到 14。管理员可见全平台数据，
普通用户的「最近查询」仅返回本人记录。慢查询阈值固定为 `execution_time_ms > 200`。

响应 `data` 结构：

```json
{
  "kpi": {
    "connections": 5,
    "today_queries": 58,
    "today_active_users": 2,
    "audit_events": 193
  },
  "connection_types": { "mysql": 2, "postgres": 2, "redis": 1 },
  "trend": [
    { "date": "2026-09-15", "total": 58, "failed": 2, "slow": 8 }
  ],
  "rank": [
    { "connection_id": 1, "name": "订单核心库", "count": 304 }
  ],
  "recent": [
    {
      "id": 12, "connection_id": 1, "user_id": 1,
      "connection_name": "订单核心库", "database_name": "orders",
      "sql_text": "SELECT ...", "status": 1,
      "affected_rows": null, "execution_time_ms": 72,
      "error_message": null, "created_at": "2026-09-15T09:46:25Z"
    }
  ],
  "days": 14
}
```

字段说明：

- `kpi.connections`：数据源总数（已纳管）；`today_queries`：当日 0 点起的查询次数；
  `today_active_users`：当日执行过查询的去重用户数；`audit_events`：审计事件累计数。
- `connection_types`：按 `mysql / postgres / redis` 分组计数，未使用的类型可能缺键。
- `trend`：按天补齐的时间序列（无数据日补 0），`total/failed/slow` 分别为查询总量、
  失败量、慢查询量。
- `rank`：时间窗口内各数据源查询量降序，最多 8 条。
- `recent`：最近查询记录，最多 8 条，结构同查询历史列表项。

### 9.2 SQL 执行运行指标（管理员）

**GET** `/api/v1/metrics/query`，或 `/api/v1/metrics/query?format=prometheus`。

仅管理员可访问：未登录返回 401，其他角色返回 403。默认返回标准 JSON 信封；
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
详细口径、Prometheus 名称和可重复的 PostgreSQL/MySQL 压测步骤见
[SQL 工作台运行指标与压测](../开发指南/SQL工作台运行指标与压测.md)。

---

## 10. 数据目录 (Assets)

> 所有接口需登录。**读接口**（overview/tree/tables/table/search/users-brief）任意登录角色可用；
> **采集结构、写入标注**仅 `admin`、`developer`，`readonly` 返回 `40300`；**收藏**任意登录用户可用。
> 结构快照由「采集结构」动作采集，人工标注独立存储，重新同步不覆盖人工内容。

### 10.1 采集结构

**POST** `/api/v1/assets/sync`（写角色，记审计）

请求体**可空**：空体 = 同步全部关系型数据源；指定单源：
```json
{ "connection_id": 1 }
```
空请求体或 `{}` 表示全量。显式 `connection_id` 必须为正整数，0、负数、null、未知字段或尾随 JSON 返回 400。

串行采集，整体超时 4 分钟；非关系型（redis 等）标记 `skipped`。每个数据源的全部库/Schema 成功采集后，一次事务发布快照和清理过期对象；发布前的读取失败、取消、超时或配置变更均保留该源原快照，返回 `status=failed`、发布计数为 0。错误聚合进 `errors`，不中断其余数据源。

同一元数据库下的同源采集跨后端互斥。单源请求重复采集返回 409；全量请求将冲突源标为 `busy` 并继续其他源。结果状态为 `succeeded | failed | busy | skipped | unknown`；提交确认丢失为 `unknown`，可能已经发布，刷新数据目录核实，不自动重试。

**Response data:**
```json
{
  "connections": [
    {
      "connection_id": 1, "connection_name": "订单核心库", "type": "postgres",
      "status": "succeeded",
      "skipped": false,
      "databases": ["orders"],
      "tables": 24, "views": 2,
      "skipped_databases": ["template0"],
      "errors": [],
      "by_database": [
        { "database": "orders", "schemas": 3, "tables": 24, "views": 2 }
      ]
    }
  ],
  "tables": 24, "views": 2
}
```

### 10.2 资产概览统计

**GET** `/api/v1/assets/overview`
```json
{
  "connection_total": 6, "table_total": 128, "view_total": 14,
  "owned_tables": 73, "sensitive_fields": 19
}
```

### 10.3 资产目录树

**GET** `/api/v1/assets/tree?connection_id=`（`connection_id` 可空=全部数据源）
```json
{
  "items": [
    {
      "id": 1, "name": "订单核心库", "type": "postgres", "environment": "prod",
      "table_count": 26, "is_empty": false,
      "databases": [
        { "name": "postgres", "table_count": 26, "schemas": [
          { "name": "shop", "table_count": 4, "tables": [
            { "name": "orders", "type": "table" },
            { "name": "v_monthly_sales", "type": "view" }
          ]}
        ]}
      ]
    },
    {
      "id": 2, "name": "未同步示例", "type": "postgres", "environment": "dev",
      "table_count": 0, "is_empty": true, "databases": []
    }
  ]
}
```
> MySQL 以库名为唯一 schema 名；PostgreSQL 过滤 `pg_catalog / information_schema / pg_toast*`。`table_count` 为聚合统计，`is_empty` 为 `table_count==0` 时前端展示“未同步/不可达”徽标与弱化样式（M2）。

### 10.4 资产列表（分页 + 过滤）

**GET** `/api/v1/assets/tables?connection_id=&database=&schema=&q=&type=&sensitivity=&no_owner=&starred=&page=1&page_size=50`

| 参数 | 说明 |
| --- | --- |
| `q` | 表名/表注释/业务说明 ILIKE 模糊匹配 |
| `type` | `table` / `view`，空为全部 |
| `sensitivity` | `normal` / `sensitive` / `confidential`（表级标注） |
| `no_owner` | `1` 仅返回未指派 Owner 的表 |
| `starred` | `1` 仅返回当前用户收藏 |
| `page_size` | 缺省 50，上限 200 |

**Response data:** `{ "items": [资产行], "total": 128, "page": 1, "page_size": 50 }`，资产行：
```json
{
  "snapshot": {
    "id": 12, "connection_id": 1, "database_name": "orders",
    "schema_name": "public", "table_name": "orders", "table_type": "table",
    "table_comment": "订单主表", "estimated_rows": 120330, "data_bytes": 98765432,
    "raw_columns": [
      { "name": "id", "ordinal": 1, "data_type": "bigint",
        "is_primary": true, "is_nullable": false, "default": "nextval(...)", "comment": "" }
    ],
    "raw_indexes": [{ "name": "orders_pkey", "columns": ["id"], "is_unique": true, "is_primary": true }],
    "raw_keys": [{ "name": "orders_customer_fk", "kind": "foreign_key",
      "columns": ["customer_id"], "ref_schema": "public", "ref_table": "customers",
      "ref_columns": ["id"] }],
    "ddl_text": "CREATE TABLE ...",
    "synced_at": "2026-09-16T10:00:00Z"
  },
  "connection_name": "订单核心库",
  "owner_user_id": 2, "owner_name": "dev1",
  "business_desc": "订单主表，记录每笔交易",
  "tags": ["核心", "交易"], "sensitivity": "confidential",
  "starred": true, "query_count_30d": 304, "sensitive_columns": 2
}
```
> `query_count_30d` 由 `sys_query_history` 近 30 天成功 SQL 按词边界正则聚合（每连接回溯上限 5000 条），
> 不另建热度表。

### 10.5 表资产详情

**GET** `/api/v1/assets/table?connection_id=&database=&schema=&table=`（四元组定位）
```json
{
  "snapshot": { /* 同 10.4 的 snapshot（含完整 raw_* 与 ddl_text）*/ },
  "connection_name": "订单核心库", "environment": "prod",
  "table_annotation": {
    "id": 1, "connection_id": 1, "database_name": "orders",
    "schema_name": "public", "table_name": "orders", "column_name": "",
    "owner_user_id": 2, "business_desc": "...", "tags": ["核心"],
    "sensitivity": "confidential", "starred_by": [1, 2],
    "updated_by": 1, "updated_at": "2026-09-16T10:00:00Z"
  },
  "column_annotations": [
    { "column_name": "total_amount", "owner_user_id": null,
      "business_desc": "订单总金额（元）", "tags": ["金额"], "sensitivity": "sensitive" }
  ],
  "starred": true, "query_count_30d": 304
}
```
不存在返回 `40400`。

### 10.6 保存人工标注

**PUT** `/api/v1/assets/annotations`（写角色，记审计）；表级/列级共接口，`column` 为空表示表级。
```json
{
  "connection_id": 1, "database": "orders", "schema": "public",
  "table": "orders", "column": "total_amount",
  "owner_user_id": 2,
  "business_desc": "订单总金额（元）",
  "tags": ["金额"],
  "sensitivity": "sensitive"
}
```
校验规则：`connection_id / database / table` 必填；`business_desc` ≤ 1000 字；
`sensitivity ∈ normal | sensitive | confidential`（缺省 normal）；`tags` 最多 8 个、单个 ≤ 20 字；
`owner_user_id` 非空时必须是存在的启用用户，否则 `40000 指定的 Owner 不存在`。
响应 `{ "ok": true }`。重复提交为 upsert（按对象五元组唯一）。

### 10.7 收藏 / 取消收藏

**POST** `/api/v1/assets/star`（任意登录用户，幂等，去重数组）
```json
{ "connection_id": 1, "database": "orders", "schema": "public", "table": "orders", "star": true }
```
响应 `{ "starred": true }`；`star=false` 取消。仅表级收藏。

### 10.8 全局搜索

**GET** `/api/v1/assets/search?q=ord`（≥2 字符，不足返回空分组；前端 250ms 防抖）
```json
{
  "connections": [{ "id": 1, "name": "订单核心库", "type": "postgres", "environment": "prod" }],
  "tables": [{ "connection_id": 1, "connection_name": "订单核心库",
    "environment": "prod", "database": "orders", "schema": "public",
    "name": "orders", "type": "table", "comment": "订单主表" }],
  "columns": [{ "connection_id": 1, "connection_name": "订单核心库",
    "database": "orders", "schema": "public", "table": "orders",
    "column": "order_no", "data_type": "varchar(32)", "comment": "订单号" }],
  "reports": [],
  "history": [{ "id": 12, "connection_id": 1, "connection_name": "订单核心库",
    "database_name": "orders", "sql_text": "SELECT ...", "created_at": "..." }]
}
```
- 连接/表/字段各最多 8 条，历史最多 6 条；普通用户仅检索本人历史，admin 可检索全部。
- 字段检索基于快照 `raw_columns`（JSONB）按列名/列注释匹配。
- `reports` 分组在 M4 报表中心落地后填充。

### 10.9 Owner 用户名簿

**GET** `/api/v1/users/brief`（任意登录用户，仅返回启用用户）
```json
{ "items": [
  { "id": 1, "username": "admin", "role": "admin" },
  { "id": 2, "username": "dev1", "role": "developer" }
] }
```

## 11. 报表中心 (Reports，M3/M4)

> 读：登录可用；写（创建/编辑/删除/收藏）：`admin`、`developer`，`readonly` 403；私有报表仅 owner/admin 可见。

### 11.1 创建报表（M3 工作台另存为报表）

**POST** `/api/v1/reports`（写角色，记审计）

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

- `name` 必填 ≤128，`sql` 必填单语句，`chart_type ∈ table|bar|line|pie|metric`，`visibility ∈ private|shared`
- `chart_config` 为 ChartCard 配置：维度/指标/聚合/排序/TopN
- 成功返回报表详情，含 `id/owner_user_id/created_at` 等

### 11.2 报表列表

**GET** `/api/v1/reports?q=&scope=mine|starred|shared|all&connection_id=&page=1&page_size=20`

- `scope=mine` 仅本人，`starred` 仅收藏，`shared` 仅共享，`all` 全部（私有仅本人/admin可见）
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

### 11.3 报表详情 / 更新 / 删除

- **GET** `/api/v1/reports/{id}`：私有报表仅 owner/admin 可见，否则 403
- **PUT** `/api/v1/reports/{id}`：仅 owner/admin 可编辑，请求体同创建（部分字段）
- **DELETE** `/api/v1/reports/{id}`：仅 owner/admin

### 11.4 报表收藏

**POST** `/api/v1/reports/{id}/star` `{ "star": true|false }` → `{ "starred": true }`，任意登录用户，幂等

### 11.5 仪表盘 CRUD（M4-FR-02）

> 读：登录可用；写：`admin`/`developer`；私有仅 owner/admin 可见；收藏任意登录用户。

- **POST** `/api/v1/dashboards` `{ name(必填≤128), description?, visibility?: private|shared, layout?: [{report_id,x,y,w,h}] }` → `{ id }`
  - `layout` 为 12 栅格：`w` 1~12，`x/y/h` 可选，M4 前端简化为报表 ID 列表组合
- **GET** `/api/v1/dashboards?q=&scope=mine|starred|shared|all&visibility=&page=&page_size=` → `{ items, total }`
  - `scope` 同报表；`items[].layout` 为原始 JSON；`starred` 布尔投影
- **GET** `/api/v1/dashboards/{id}`：私有校验 owner/admin
- **PUT** `/api/v1/dashboards/{id}`：仅 owner/admin，支持改名/描述/可见性/布局
- **DELETE** `/api/v1/dashboards/{id}`：仅 owner/admin，级联逻辑由前端清理分享
- **POST** `/api/v1/dashboards/{id}/star` `{ starred: bool }` → `{ starred }`

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

### 11.6 分享与公开只读页（M4-FR-03）

> 创建/列表/吊销/删除需登录；写角色才能创建分享，且仅 owner/admin 可分享自己的报表/仪表盘；公开页 `/api/v1/public/s/{token}` 免登录。

- **POST** `/api/v1/shares`（写角色，记审计）
```json
{ "subject_type": "report|dashboard", "subject_id": 1, "expire_days": 1|7|30|null }
```
`expire_days` 空=永久；服务端生成 24 字节 hex token，仅存储 SHA-256 哈希；明文仅创建时返回一次。

响应：

```json
{ "id": 1, "subject_type": "report", "subject_id": 1, "token": "a1b2c3...", "expire_at": "2026-09-27T10:00:00Z", "created_at": "2026-09-20T10:00:00Z" }
```

- **GET** `/api/v1/shares?subject_type=report&subject_id=1` → `{ items: [{ id, subject_type, subject_id, expire_at, access_count, revoked, created_at }] }`
- **POST** `/api/v1/shares/{id}/revoke` → `{ id, revoked: true }`
- **DELETE** `/api/v1/shares/{id}` → `{ id }`

- **GET** `/api/v1/public/s/{token}`（免登录，无需 Bearer）
  - 校验：hash 是否存在、是否 revoked、是否过期（`expire_at`）
  - 访问计数异步 `access_count+1`
  - 响应：

```json
{
  "share_id": 1,
  "subject_type": "dashboard",
  "subject": { "id": 1, "name": "销售总览", "layout": [...], "reports": [{ "id":1,"name":"月度销售","chart_type":"bar", ... }] },
  "owner_name": "admin",
  "expire_at": "2026-09-27T10:00:00Z",
  "access_count": 5
}
```

报表分享 `subject` 为 Report 详情；仪表盘分享 `subject` 包含 `layout` 与 `reports` 快照（便于公开页无需二次鉴权即可渲染）。过期/吊销/不存在均返回 `40400`。

### 11.7 前端路由

- `/reports` 报表中心（M3）
- `/dashboards` 仪表盘列表/查看/编辑/分享（M4，12 栅格、刷新全部、收藏）
- `/s/:token` 公开只读分享页（M4，免登录，暗色玻璃卡片+ChartCard 只读）

### 11.8 工作台一键另存为报表（Step23）

> **前端**：`QueryView` 工具栏 `另存报表`（`BookmarkPlus`），禁用态 `!rows.length`，弹框完成 `POST /api/v1/reports`：
```json
{
  "name": "报表 2026/10/14",
  "description": "",
  "connection_id": 1,
  "database": "orders",
  "sql": "SELECT ...",
  "chart_type": "table",
  "chart_config": { "dimension": "id", "metrics": ["total_amount"], "aggregation": "sum" },
  "visibility": "private"
}
```
`chart_config` 由结果首列为维度、次列为指标自动生成，前端可编辑；返回后提示“已另存为报表，可在报表中心查看”。
`route.query.sql` 支持从仪表盘监控 `PerformanceMonitorPane` 一键回跳工作台重放。

---

## 12. 数据资产血缘（Lineage，M5，Step22）

> **前端**：`AssetView` 详情抽屉 `血缘` Tab 复用 `DataLineagePane.vue`，传入 `current-table/current-db`，Mock 上下游；`@create-ticket` 一键跳转工单中心。

### 12.1 规划后端（M5 预留）

- **GET** `/api/v1/assets/lineage?connection_id=&database=&schema=&table=&depth=1|2|all`
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
  - 合并实时快照外键与 `sys_query_history` 解析（`pg_query_go` / `vitess/sqlparser`）+ 仪表盘引用，缓存表 `sys_meta_lineage`（见 DB 设计 2.10 规划）。

### 12.2 前端 Mock（Step22 现状）

`DataLineagePane` 内置 Mock 以 `orders` 为中心生成 `customers/products → orders → order_items → 报表/看板` 链路，含深度切换、影响清单、高亮当前表、创建工单快捷入口。

---

## 13. 仪表盘性能监控复用（Step23）

> **前端**：`DashboardView` 新增 `查询性能监控` 板块（`h 520px` 玻璃卡片），复用 `PerformanceMonitorPane.vue`（非复制），与 SQL 工作台同源指标：
- KPI：QPS、平均/ P95 耗时、慢查询数、连接池、缓存命中
- 图：趋势、库排行、慢查询列表
- 能力：`@reuse` → `router.push({ path:'/query', query:{ sql } })` 回跳工作台重放；`前往工作台` 快捷

> 后端：复用 `GET /api/v1/metrics/overview?days=14` 与 `query/history`，无需新增接口。

---

## 14. 工单中心（Tickets，Step24 前端 Mock，后端规划 M6）

> **前端**：独立页面 `/tickets`（`TicketView`），侧栏 `数据` 分组 `工单中心`（`Ticket`），`glass-card` + KPI 5 卡，`localStorage dbhub_tickets_v1` 持久化，`ChangeTicketPane` 复用。

### 14.1 组件契约（现为前端 Mock）

**ChangeTicketPane Props / Emits**
```ts
props: {
  tickets: TicketItem[]      // { id, title, type:'ddl'|'dml'|'index', table, sql, risk:'low'|'medium'|'high', status:'pending'|'approved'|'rejected'|'executed', requester, reason, created_at }
  prefill?: { title?, table?, sql?, risk?, reason?, type? } | null
}
emits: (e:'create', payload: Omit<TicketItem,'id'|'status'|'requester'|'created_at'>) | (e:'updateStatus', id:number, status:string)
```
- `prefill` 变更时 watch 自动填充表单并 `showCreate=true`
- 列表支持状态筛选（全部/待审批/已批准/已执行/已拒绝）+ 关键字（标题/表名/SQL）+ 复制 SQL

**TicketView 交互**
- `GET /tickets?table=&db=&title=&sql=&risk=`：资产血缘一键建单入口；`incoming` 横幅 + `一键建单` 预填 `ALTER TABLE {{table}}`
- 头部操作：加载示例、清空已办（仅保留 pending/approved）

### 14.2 规划后端（M6 预留，详见 DB 设计 2.10）

- **GET** `/api/v1/tickets?status=&q=&page=&page_size=` → `{ items, total }`
- **POST** `/api/v1/tickets` `{ title, type, table, sql, risk, reason }` → `{ id }`（记审计）
- **POST** `/api/v1/tickets/{id}/status` `{ status:'approved'|'rejected'|'executed' }`（`admin/developer` 审批，`readonly` 403）
- 审计：创建、审批、执行均记 `sys_audit_logs`，字段 `resource_type=ticket`

### 14.3 关联链路

- 资产血缘 `DataLineagePane @create-ticket` → `router.push /tickets?table=&db=&title=` → 工单中心预填
- 未来：工单执行将联动 `sys_query_history` 与 `sys_meta_snapshots` 变更对比

---

## 15. SQL 工作台精简与复用（Step21-Step24）

> **精简**：工作台保留 `结果 / 统计 / 快照 / AI 优化 / 计划对比 / 火焰图 / 对比 / 队列 / 限制 / 脱敏 / 权限 / 历史 / Redis / 消息` 13 Tab，剥离 `图表/监控/工单` 至归宿模块，保证导航与路由一一对应、无假链接。
- 图表 → 报表中心 `另存报表`（11.8）
- 监控 → 仪表盘 `查询性能监控`（13）
- 工单 → 工单中心（14）
- 血缘 → 数据资产详情 `血缘` Tab（12）




## 连接器目录、自定义数据源与能力

所有接口使用 `Authorization: Bearer <access_token>`，沿用统一 envelope。驱动与模板写入仅 admin，写入接受严格 JSON、记审计；普通登录用户只读模板与驱动摘要，不返回路径或凭据。

| 方法/路径（/api/v1） | 行为 |
| --- | --- |
| GET /connector-drivers/available | admin 读取 JDBC 服务实际可加载、校验通过的制品 |
| GET /connector-drivers | 已登记驱动摘要列表 |
| POST /connector-drivers | admin 登记 `{ "artifact_id":"postgresql-42.7.7", "name":"PostgreSQL JDBC" }`；重复制品 409 |
| PATCH /connector-drivers/{id}/status | admin `{ "enabled": false }`；停用阻止新会话，不重派已有执行 |
| DELETE /connector-drivers/{id} | admin 删除未引用版本，引用中返回 409 |
| GET /datasource-templates | 登录用户读取模板列表；admin 可包含停用版本 |
| POST /datasource-templates | admin 发布第一个不可变修订 |
| PUT /datasource-templates/{id} | admin 按相同 key 发布新修订，不改旧实例 |
| PATCH /datasource-templates/{id}/status | admin 启停指定修订 |
| DELETE /datasource-templates/{id} | admin 删除未引用修订，引用中返回 409 |
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
