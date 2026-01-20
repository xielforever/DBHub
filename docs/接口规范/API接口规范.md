# 接口规范 (API Specification)

本规范定义了 DBHub 后端接口的交互标准。

## 1. 认证模块 (Auth)

### 1.1 用户登录
**POST** `/api/v1/auth/login`

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
  "data": {
    "token": "eyJhbGciOiJIUzI1Ni...",
    "expire_at": "2026-01-21T10:00:00Z",
    "user": {
      "id": 1,
      "username": "admin",
      "role": "admin"
    }
  }
}
```

### 1.2 刷新令牌
**POST** `/api/v1/auth/refresh`

使用 Header 中的旧 Token 换取新 Token（如果在刷新窗口期内）。

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
