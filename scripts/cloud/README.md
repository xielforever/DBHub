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

# 第一阶段工作台的执行状态回归测试与前端构建
./scripts/cloud/dev.sh run --rm --no-deps frontend sh -ec 'npm run test:query; npm run build'

# 单实例执行容量基线：独立容器、随机测试对象，结束后清理
./scripts/cloud/benchmark.sh

# 数据源版本矩阵：逐个临时数据库，真实操作与 HTTP 验证
python3 scripts/cloud/compatibility.py
# 可先单独验证一个版本；此报告不能单独改写完整支持清单
python3 scripts/cloud/compatibility.py --engine mysql --branch 5.7
# 完整矩阵遇到环境故障时，仅补跑失败项；后端实现必须未变化
python3 scripts/cloud/compatibility.py --resume-from /私有报告路径/matrix.json

# 查看服务状态和日志；停止进程但保留数据库卷
./scripts/cloud/dev.sh ps
./scripts/cloud/dev.sh logs --tail 40 backend frontend
./scripts/cloud/dev.sh stop
```

查询队列和活动目标事务属于单个后端进程，重启后活动事务由数据库连接关闭回滚；已经提交的自动提交写入不因此回滚。跨实例部署需要会话路由或共享调度器。SQL 执行编号与回执保存在 PostgreSQL，独立处理器使用同一元数据库时也能防止相同编号重复执行；该能力不提供跨实例并发总量限制。在线心跳和认证会话使用 Redis，业务表数据和任意 SQL 查询结果不进入 Redis 缓存。完整接口与支持边界见 [补齐验收契约](../../docs/开发指南/云端后端补齐验收.md) 和 [执行可靠性](../../docs/开发指南/SQL工作台执行可靠性.md)。

可选浏览器验收：先启动服务，再运行 `./scripts/cloud/browser.sh`。浏览器依赖安装在临时容器中，截图和验收结果保存到私有状态目录 `browser-artifacts`，本轮文件以 `phase1-` 开头。检查真实 PostgreSQL/MySQL 查询、格式化、选中执行、单请求与真实取消、错误、截断、虚拟滚动和手机首屏；还验证刷新后恢复待确认编号、丢失写入响应后只查询回执而不重跑，并断言工作台不请求已暂缓的高级功能。脚本仅向本地 Cloud 开发数据库写入 `dbhub_cloud_demo` 验证表。

生产构建验收使用独立项目，不复用开发项目的凭据、元数据库或 Redis。先构建 `dbhub-backend:phase1-release` 与 `dbhub-frontend:phase1-release`，再运行：

```sh
./scripts/cloud/release.sh up -d --wait --wait-timeout 180
./scripts/cloud/release.sh run --rm --no-deps browser
./scripts/cloud/release.sh stop
```

生产 Compose 为根目录 `docker-compose.prod.yaml`；Cloud 额外合并 `release.compose.yaml` 提供本地依赖。验收状态保存在 `/workspace/dbhub-release-check`，数据库临时文件保存在 `${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}/release-data`，不能用作生产持久化路径。浏览器经 Nginx 访问真实 production 后端，额外覆盖角色权限、会话撤销、生产目标写入确认、62 秒长查询与超时原因指标。构建、后端替换核实、备份和回滚步骤见 [部署运维指南](../../docs/部署运维/部署运维指南.md)。

版本矩阵会使用当前开发后端容器执行 Go 竞态测试，基础 PostgreSQL/MySQL/Redis 必须已启动。目标版本逐个拉取、启动并清理，数据放在容器临时内存文件系统；默认删除本轮新拉取的镜像，保留运行前已有镜像，`--keep-images` 可保留本轮镜像。不要并行运行矩阵或重建后端；VFS 环境也应避免同时执行其他大量磁盘写入的构建。

报告在私有 `compatibility-artifacts/<UTC时间>/matrix.json`，记录真实补丁号、镜像摘要、测试名称、跳过与清理状态，以及后端 Go 实现和迁移 SQL 的独立指纹。PostgreSQL 各分支额外执行数据源检查、并发编辑、采集失败/互斥/取消的可靠性测试。完整有效报告可通过 `python3 scripts/cloud/compatibility.py --record-from /私有报告路径/matrix.json` 更新内嵌版本记录；部分报告、失败或任一实现/迁移指纹变化时拒绝更新。恢复运行会记录原报告摘要和复用项来源，不会掩盖原运行的失败。`.github/workflows/datasource-compatibility.yml` 提供手动触发的完整 CI 验证，不上传凭据文件。具体范围见 [数据源版本兼容](../../docs/开发指南/数据源版本兼容.md)。

版本提示的浏览器验收：`DBHUB_BROWSER_TEST_SCRIPT=/app/scripts/cloud/version_browser_test.py ./scripts/cloud/browser.sh`。验证真实数据库版本显示、修改连接后清除旧结果、过期响应处理及手机表单；MariaDB 警示使用界面契约样例，不代表真实产品兼容验证。

数据源异常与并发的定向回归：`./scripts/cloud/dev.sh exec -T backend go test -race -count=1 ./internal/server -run '^(TestDatasourceReliability|TestMetadataSyncReliability)$'`。测试会创建随机 Schema、临时账号与连接，在独立目标账号上设置 statement timeout 并对测试表加锁，验证采集失败和取消；不修改现有账号的权限。行为与原始结果见 [检查与采集可靠性](../../docs/开发指南/数据源检查与采集可靠性.md)。
