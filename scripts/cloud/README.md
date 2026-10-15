# Cloud 开发环境

使用 `./scripts/cloud/setup.sh` 安装并测试，使用 `./scripts/cloud/start.sh` 启动真实后端和前端。需要宿主 Docker、Compose、Python 3；应用依赖安装、构建、测试均在容器中进行。PostgreSQL、MySQL、Redis 的端口仅在容器网络开放，前后端开发端口只绑定宿主回环地址。

脚本继承会话 HTTP/HTTPS 代理，将平台 CA 只读挂载进容器，并动态解析代理主机；不关闭证书或模块校验。Node、Go 基础镜像固定摘要，PG AST 解析需要带 GCC 的 Go 镜像及 CGO。Go 编译缓存和临时文件放在 `${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}`，避免占满 Cloud 持久层。代码依赖使用已提交 vendor，可离线编译；依赖变更后在容器中运行 `backend/scripts/vendor.sh`，脚本会补回 Go vendor 默认遗漏的 libpg_query C 头文件。

本地开发凭据首次生成到 `${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}/runtime.env`，文件权限 0600，目录 0700，均位于代码仓之外。不要提交或打印该文件，不要输出展开后的 Compose 配置；管理员账号为 admin，开发口令可在该私有文件中查看。首次生成后重复启动不会轮换凭据。生产环境使用平台密钥管理注入应用密钥、管理员密码、元数据库和 Redis 配置，不能使用此本地开发文件。

AI 暂不需要配置。以后通过云环境变量设置 `DBHUB_AI_BASE_URL`、`DBHUB_AI_MODEL`、`DBHUB_AI_API_KEY`，再运行 `./scripts/cloud/dev.sh up -d --no-deps --force-recreate backend`。接口只接受 HTTPS，使用系统/平台 CA 校验服务端证书。AI 请求须由用户主动发起，服务端不会自动执行生成 SQL。

常用命令：

```sh
# 真实 PostgreSQL、MySQL、Redis 集成测试（环境变量由 wrapper 注入）
./scripts/cloud/dev.sh up -d --wait postgres mysql redis
./scripts/cloud/dev.sh run --rm --no-deps backend go test -p=4 ./...

# 并发竞态检查与静态检查
./scripts/cloud/dev.sh run --rm --no-deps backend go test -race -p=4 ./...
./scripts/cloud/dev.sh run --rm --no-deps backend go vet -p=4 ./...

# 查看服务状态和日志；停止进程但保留数据库卷
./scripts/cloud/dev.sh ps
./scripts/cloud/dev.sh logs --tail 40 backend frontend
./scripts/cloud/dev.sh stop
```

查询队列和活动目标事务属于单个后端进程，重启后事务由数据库连接关闭回滚；跨实例部署需要会话路由或共享调度器。在线心跳和认证会话使用 Redis，SQL 片段、策略、分享、导出申请与工单使用 PostgreSQL。完整接口与支持边界见 [补齐验收契约](../../docs/开发指南/云端后端补齐验收.md)。

可选浏览器验收：先启动服务，再运行 `./scripts/cloud/browser.sh`。浏览器依赖安装在临时容器中，截图和验收结果保存到私有状态目录 `browser-artifacts`。脚本仅向本地 Cloud 开发数据库写入 `dbhub_cloud_demo` 验证表。
