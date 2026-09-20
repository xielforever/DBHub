# Arena 专属启动脚本

沙箱每次重启后文件保留但进程丢失，需手动拉起服务。

## 一键启动

```bash
bash scripts/arena-start.sh
```

脚本逻辑：
1. 检查 Go 是否可用（`/usr/local/go/bin/go`、`/opt/go/bin/go`、`$HOME/sdk/go/bin/go`、`which go`）：
   - 可用且端口空闲 → `go run ./cmd/server` 启动真实后端（内存模式，`DATABASE_URL` 可空），日志 `logs/backend.log`
   - 不可用或启动失败 → 回退 Mock 后端
2. Mock 后端 `scripts/mock-backend.mjs`（Node 原生 http，无依赖）监听 `0.0.0.0:8080`，覆盖前端所需全部 `/api` 路由，返回符合 API 契约的假数据，支持报表/仪表盘/分享 CRUD 与公开页。
3. 前端 `frontend` `VITE_API_PROXY_TARGET=http://localhost:8080 npm run dev -- --port 5173 --host 0.0.0.0`，`vite.config.ts` 已配置 `host:true, allowedHosts:true`，适配 Arena 预览网关。

启动后：
- Mock/Real 后端: http://localhost:8080  (`/api/health`)
- 前端 Vite: http://localhost:5173
- Arena 预览: `https://5173-{SANDBOX_ID}.e2b.app` 与 `https://8080-{SANDBOX_ID}.e2b.app`（平台自动注入）

## 停止

```bash
bash scripts/arena-stop.sh
```

## 日志

```bash
tail -f logs/mock-backend.log logs/frontend.log logs/backend.log
```

## 为什么需要 Mock 后端？

- 沙箱网络限制：`dl.google.com/go`、`proxy.golang.org`、`goproxy.cn`、`docker registry` 全灭，无法下载 Go 二进制与镜像；
- `apt` 无权限，无法 `apt install golang`；
- `hxjiang-go` npm 包仅含源码树，无预编译产物；
- 真实后端需 PostgreSQL，沙箱无持久 DB。

Mock 后端用 Node 实现，无外部依赖，`registry.npmjs.org` 可达，适合 Arena 快速 UI 审查。

## 切换真实后端

若沙箱后续提供 Go：

```bash
export DATABASE_URL="postgres://user:pass@host:5432/dbhub?sslmode=disable"
export SECRET_KEY="32+ bytes"
bash scripts/arena-start.sh
```

脚本会自动优先启动真实后端，Mock 不启动。

## 与 Arena Process 工具协同

`arena-start.sh` 使用 `nohup`，适合 SSH/终端手动拉起。若需在 Arena UI 显示 LIVE PREVIEW，请用 Process 工具：

- Mock Backend: `node scripts/mock-backend.mjs`
- Frontend: `VITE_API_PROXY_TARGET=http://localhost:8080 npm run dev`

两者均需 `host 0.0.0.0`。
