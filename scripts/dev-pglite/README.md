# 开发沙箱：PGlite 线协议服务

在**无法运行 Docker/PostgreSQL 的开发沙箱**中，用进程内
[PGlite](https://github.com/electric-sql/pglite) 通过
[pglite-server](https://www.npmjs.com/package/pglite-server) 暴露标准
PostgreSQL 线协议（默认 `127.0.0.1:5432`），让 Go 后端无需真实 Postgres
即可完成本地联调与无头验证。

> ⚠️ **仅限开发/测试**。生产与正式交付一律使用仓库根目录
> `docker-compose.yaml` 中的真实 PostgreSQL，本目录脚本不参与镜像构建。

## 组成

| 文件 | 作用 |
| --- | --- |
| `server.mjs` | 启动线协议服务；启动时自动执行后端 `migrations/*.sql`、写入演示账号、灌入 `shop` 演示业务库（全部幂等） |
| `patch-pglite-server.mjs` | 幂等补丁：上游握手只回传 `server_version` 参数，补全 `standard_conforming_strings=on` 等 ParameterStatus，否则 pgx simple 协议拒绝执行（server.mjs 已自动引用，无需单独运行） |
| `seed-demo-shop.mjs` | 演示业务库：独立 schema `shop`（customers/products/orders/order_items，含外键与索引）。作为模块被 server 启动时调用；也可在**服务停止后**独立执行以重建 |
| `seed-connections.mjs` | 通过后端 API 幂等预置 6 条团队共享演示数据源（2 条指向本机可连、4 条内网不可达用于演示中文报错） |

## 启动顺序

```bash
cd scripts/dev-pglite
npm install

# 1) 启动线协议服务（自动建表 + 演示账号 + shop 库）
npm start

# 2) 另开终端，启动后端（单连接，避免线服务单会话交错）
cd ../../backend
DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  META_DB_MAX_CONNS=1 APP_SECRET_KEY=dev-only-change-me ./tmp/server

# 3) 预置演示数据源（后端启动后执行，可重复运行）
cd ../scripts/dev-pglite
npm run seed:connections
```

演示账号：`admin/admin123`、`dev1/dev12345!`、`ro1/ro123456`。
目标演示库口令均为 `postgres`。

## 注意事项

- 线协议服务为**单会话**实现，后端元数据库连接池必须 `META_DB_MAX_CONNS=1`；
  目标业务库连接为请求级短连接，不受影响。
- 禁止两个 PGlite 进程同时打开同一 `data/` 目录（会导致数据文件损坏）；
  重建 `shop` 前必须先停掉 `server.mjs`。
- 后端 pgx 统一使用 simple 协议（参数由驱动客户端转义，仍为参数化查询），
  因此补丁必须回传 `standard_conforming_strings=on`。
- `data/` 与 `node_modules/` 不入库；删除 `data/` 后重启即可获得全新环境。
