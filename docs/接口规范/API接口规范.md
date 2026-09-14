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
