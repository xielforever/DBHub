# 接口规范 (API Specification)

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

### 1.4 健康检查
**GET** `/api/health`（无需认证，供容器探针使用）

```bash
curl http://localhost:8080/api/health
```

### 1.5 统一错误码

| HTTP | code   | 含义         |
|------|--------|--------------|
| 400  | 40000  | 请求参数错误 |
| 401  | 40100  | 未认证/令牌失效 |
| 403  | 40300  | 无权限       |
| 404  | 40400  | 资源不存在   |
| 409  | 40900  | 资源冲突     |
| 500  | 50000  | 服务器内部错误（细节仅写日志，不返回前端） |

---

## 2. 连接管理 (Connection)

### 2.1 测试连接
**POST** `/api/v1/connections/test`

不保存配置，仅尝试建立连接以验证有效性。

**Request:**
```json
{
  "type": "mysql",
  "host": "192.168.1.10",
  "port": 3306,
  "username": "root",
  "password": "password",
  "ssh_tunnel_id": null
}
```

### 2.2 创建连接
**POST** `/api/v1/connections`

**Request:**
`password` 字段在传输时建议使用 RSA 公钥加密，防止中间人攻击（可选高安全模式）。

---

## 3. 数据库操作 (Database Ops)

### 3.1 获取数据库列表
**GET** `/api/v1/db/:connection_id/databases`

### 3.2 执行 SQL 查询
**POST** `/api/v1/db/:connection_id/query`

**Request:**
```json
{
  "sql": "SELECT * FROM users WHERE status = ? LIMIT ?",
  "args": [1, 10],  // 参数化查询参数
  "database": "app_db"
}
```

**Response:**
```json
{
  "code": 0,
  "data": {
    "columns": ["id", "username", "created_at"],
    "rows": [
      [1, "alice", "2026-01-01T12:00:00Z"],
      [2, "bob", "2026-01-02T13:30:00Z"]
    ],
    "affected_rows": 0,
    "execution_time_ms": 45
  }
}
```

> **安全警告**: 禁止直接凭借接字符串构建 SQL。必须通过 `args` 数组传递参数以防止 SQL 注入。

---

## 4. WebSocket 实时接口

用于长时间运行的任务（如大表迁移、日志流）或实时通知。

**Endpoint**: `/ws`

**Events:**
- `server:health`: 推送服务器负载
- `task:progress`: 推送异步任务进度 (taskId, percentage, message)

---

## 2. 数据源管理 (Connections)

> 除标注外均需 Bearer Access Token。写操作（POST/PUT/DELETE）仅 `admin`、`developer` 角色可用，`readonly` 返回 `40300`。
> 普通用户仅能访问自己创建的数据源，访问他人资源返回 `40400`（防枚举）；`admin` 可见全部。

### 2.1 连接列表
**GET** `/api/v1/connections?type=postgres&keyword=prod`

**Response data:**
```json
{
  "items": [
    {
      "id": 1, "user_id": 1, "ssh_tunnel_id": null,
      "name": "订单库", "type": "mysql", "host": "10.0.0.11", "port": 3306,
      "database": "orders", "username": "app", "ssl_mode": "require",
      "connection_timeout": 10, "color_label": "",
      "has_password": true, "tunnel_name": "",
      "created_at": "2026-09-15T04:00:00Z", "updated_at": "2026-09-15T04:00:00Z"
    }
  ],
  "total": 1
}
```
> 接口**绝不回传口令明文或密文**，仅返回 `has_password` 布尔标志。

### 2.2 创建连接
**POST** `/api/v1/connections`
```json
{
  "name": "订单库", "type": "mysql", "host": "10.0.0.11", "port": 3306,
  "database": "orders", "username": "app", "password": "s3cret",
  "ssh_tunnel_id": null, "ssl_mode": "require",
  "connection_timeout": 10, "color_label": ""
}
```
`type` 仅允许 `mysql | postgres | redis`；口令经 AES-256-GCM 加密后落库。成功返回 `201` 与连接详情。

### 2.3 修改连接
**PUT** `/api/v1/connections/{id}`，请求体同创建。`password` 留空表示保留原口令不修改。

### 2.4 删除连接
**DELETE** `/api/v1/connections/{id}`，级联删除其查询历史。

### 2.5 测试连接
- **POST** `/api/v1/connections/test`：请求体同创建（用于保存前验证，口令不落库）
- **POST** `/api/v1/connections/{id}/test`：使用已保存凭据测试

**Response data:**
```json
{ "ok": true, "type": "postgres", "version": "PostgreSQL 16.4 ...", "elapsed_ms": 48 }
```
失败返回 `40000`，消息为清洗后的驱动错误（不含口令片段）。

---

## 3. SSH 隧道 (SSH Tunnels)

### 3.1 列表 / 创建 / 修改 / 删除
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

### 3.2 测试跳板连通
**POST** `/api/v1/ssh-tunnels/test`，请求体同上（支持保存前验证），成功返回 `{ "ok": true }`。

---

## 4. SQL 工作台 (Query & Metadata)

### 4.1 执行 SQL
**POST** `/api/v1/query/execute`
```json
{ "connection_id": 1, "database": "orders", "sql": "SELECT id, name FROM users LIMIT 100" }
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
- 一次只允许执行一条语句（多语句返回 `40000`，防止批量注入）；
- 单次结果最多返回 **1000 行**，超出 `truncated=true`；
- 语句执行超时 30 秒；
- `readonly` 角色仅允许查询类语句，且 PostgreSQL 强制服务端只读事务；
- 无论成功失败均写入查询历史（失败含 `error_message`）。

### 4.2 对象浏览（均为 GET）
| 接口 | 说明 |
| --- | --- |
| `/metadata/databases?connection_id=` | 逻辑库列表（自动过滤系统库） |
| `/metadata/schemas?connection_id=&database=` | PostgreSQL 模式列表 |
| `/metadata/tables?connection_id=&database=&schema=` | 表与视图（`type=table/view`） |
| `/metadata/columns?connection_id=&database=&schema=&table=` | 列结构（类型/可空/主键/默认值/序号） |

### 4.3 表数据分页预览
**GET** `/api/v1/data/preview?connection_id=1&database=orders&schema=public&table=users&page=1&page_size=50`
```json
{ "columns": [...], "rows": [[...]], "total": 1320, "page": 1, "page_size": 50, "has_more": true }
```
> 表名/模式名经服务端标识符转义（PG 双引号、MySQL 反引号），杜绝表名注入。

### 4.4 Redis 浏览（GET）
| 接口 | 说明 |
| --- | --- |
| `/redis/overview?connection_id=` | 版本/模式/运行天数/客户端数/内存/键空间统计 |
| `/redis/keys?connection_id=&pattern=user:*&limit=200` | SCAN 游标遍历键（默认上限 500），含类型与 TTL |
| `/redis/value?connection_id=&key=k` | 按类型预览值（string/list/hash/set/zset，大元素截断 100 项） |

### 4.5 查询历史
- **GET** `/api/v1/query/history?connection_id=&status=success|failed&page=1&page_size=20`
  → `{ items, total, page, page_size }`，普通用户仅本人记录，admin 全部；
- **DELETE** `/api/v1/query/history/{id}`：删除单条（仅本人/admin）；
- **DELETE** `/api/v1/query/history`：清空本人历史（admin 清空全部）。

---

## 5. 用户与角色 RBAC（仅 admin）

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

### 5.1 修改自己的密码（任意登录用户）
**POST** `/api/v1/auth/change-password`
```json
{ "old_password": "...", "new_password": "至少 8 位" }
```
原密码错误返回 `40000`；新密码不得与原密码相同。

### 5.2 角色权限矩阵
| 能力 | admin | developer | readonly |
| --- | --- | --- | --- |
| 仪表盘/查看数据源 | ✅ | ✅ | ✅ |
| 数据源/隧道增删改、测试连接 | ✅ | ✅ | ❌（403） |
| 执行 SELECT / SHOW / EXPLAIN 等查询 | ✅ | ✅ | ✅（PG 强制只读事务） |
| 执行 INSERT/UPDATE/DDL 等写操作 | ✅ | ✅ | ❌（400 拒绝） |
| 用户与角色管理、审计日志 | ✅ | ❌ | ❌ |
| 修改自己的密码 | ✅ | ✅ | ✅ |

---

## 6. 审计日志（仅 admin）

**GET** `/api/v1/audit-logs?days=7&username=&action=&resource_type=&status=success|failed&page=&page_size=`

关键写操作（登录、改密、连接/隧道/用户增删改、测试连接、SQL 执行、历史清理）
由审计中间件自动落库，记录操作者、来源 IP、User-Agent、结果状态、耗时与 trace_id；
请求体不落库以防泄密。返回 `{ items, total, page, page_size }`。
`action` 取值：`LOGIN / CHANGE_PASSWORD / CREATE / UPDATE / DELETE / TEST / QUERY`。

## 7. 仪表盘指标（Metrics）

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
