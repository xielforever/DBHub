# Cloud 开发环境

当前启动步骤同时启用每日元数据备份，维护私有 libpq 配置卷和备份卷。通知、后台任务及部署参数见[实施与验收](../../docs/开发指南/运维通知与数据源任务验收.md)。审计与查询历史自动清理默认关闭。

使用 `./scripts/cloud/setup.sh` 安装并测试，使用 `./scripts/cloud/start.sh` 启动真实后端和前端。需要宿主 Docker、Compose、Python 3；应用依赖安装、构建、测试均在容器中进行。PostgreSQL、MySQL、Redis、JDBC 的端口仅在容器网络开放，前后端开发端口只绑定宿主回环地址。

脚本继承会话 HTTP/HTTPS 代理，将平台 CA 只读挂载进容器，并动态解析代理主机；不关闭证书或模块校验。Node、Go 基础镜像固定摘要，PG AST 解析需要带 GCC 的 Go 镜像及 CGO。Go 编译缓存和临时文件放在 `${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}`，避免占满 Cloud 持久层。代码依赖使用已提交 vendor，可离线编译；依赖变更后在容器中运行 `backend/scripts/vendor.sh`，脚本会补回 Go vendor 默认遗漏的 libpg_query C 头文件。

本地开发凭据首次生成到 `${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}/runtime.env`，文件权限 0600，目录 0700，均位于代码仓之外。不要提交或打印该文件，不要输出展开后的 Compose 配置；管理员账号为 admin，开发口令可在该私有文件中查看。首次生成后重复启动不会轮换凭据。生产环境使用平台密钥管理注入应用密钥、管理员密码、元数据库和 Redis 配置，不能使用此本地开发文件。

AI 暂不需要配置。以后通过云环境变量设置 `DBHUB_AI_BASE_URL`、`DBHUB_AI_MODEL`、`DBHUB_AI_API_KEY`，再运行 `./scripts/cloud/dev.sh up -d --no-deps --force-recreate backend`。接口只接受 HTTPS，使用系统/平台 CA 校验服务端证书。AI 请求须由用户主动发起，服务端不会自动执行生成 SQL。

常用命令：

```sh
# 真实 PostgreSQL、MySQL、Redis、JDBC 集成测试（环境变量由 wrapper 注入）
./scripts/cloud/dev.sh up -d --wait --wait-timeout 240 postgres mysql redis jdbc
./scripts/cloud/dev.sh run --rm --no-deps backend go test -p=4 ./...

# 并发竞态检查与静态检查
./scripts/cloud/dev.sh run --rm --no-deps backend go test -race -p=4 ./...
./scripts/cloud/dev.sh run --rm --no-deps backend go vet -p=4 ./...

# Go 已导入包漏洞门禁（固定 govulncheck 版本，需要访问 vuln.go.dev）
./scripts/cloud/dev.sh run --rm --no-deps backend sh scripts/vulncheck.sh

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

统一回归入口：`python3 scripts/cloud/regression.py --profile smoke` 检查关键业务链路，`--profile full` 执行后端全量竞态、静态检查、Go 已导入包漏洞门禁和前端审计/回归/构建；`--profile release --soak-seconds 1800` 再构建当前提交的生产镜像，执行浏览器、故障恢复、完整备份恢复及真实时钟持续负载。使用 `--plan` 可预览命令。业务用例、人工验收模板、未执行项与生产准入条件见[生产上线评估与回归测试](../../docs/开发指南/生产上线评估与回归测试.md)。结果在私有状态目录 `regression-artifacts/<本次运行>/summary.json`；原始日志保留在同目录，仅共享汇总记录。

Go 门禁使用官方模块名与固定的 Go 1.26.9 / govulncheck v1.9.0，拒绝模块路径替换；已导入包存在公告、工具失败或漏洞库访问失败都会导致回归失败。仅模块存在但未导入的包会保留告警，不计作已使用的漏洞包。不要把 JSON 模式的退出码 0 当作没有漏洞。需要通过云环境网络设置允许 `vuln.go.dev` 并等待运行策略生效；沿用平台代理和 CA。修复与剩余告警说明见[Go 依赖安全修复与验收](../../docs/开发指南/Go依赖安全修复与验收.md)。

查询队列和活动目标事务属于单个后端进程，重启后活动事务由数据库连接关闭回滚；已经提交的自动提交写入不因此回滚。跨实例部署需要会话路由或共享调度器。SQL 执行编号与回执保存在 PostgreSQL，独立处理器使用同一元数据库时也能防止相同编号重复执行；普通工作台仍不提供跨实例并发总量限制；本期受管报表及仪表盘另使用 PostgreSQL 共享租约，覆盖用户、账号、工作空间和全局配额，跨连接池已验证，完整多实例运行仍需验收。在线心跳和认证会话使用 Redis，业务表数据和任意 SQL 查询结果不进入 Redis 缓存。完整接口与支持边界见 [补齐验收契约](../../docs/开发指南/云端后端补齐验收.md) 和 [执行可靠性](../../docs/开发指南/SQL工作台执行可靠性.md)。

可选浏览器验收：先启动服务，再运行 `./scripts/cloud/browser.sh`。浏览器依赖安装在临时容器中，截图和验收结果保存到私有状态目录 `browser-artifacts`，本轮文件以 `phase1-` 开头。检查真实 PostgreSQL/MySQL 查询、格式化、选中执行、单请求与真实取消、错误、截断、虚拟滚动和手机首屏；还验证刷新后恢复待确认编号、丢失写入响应后只查询回执而不重跑，并断言工作台不请求已暂缓的高级功能。脚本仅向本地 Cloud 开发数据库写入 `dbhub_cloud_demo` 验证表。

生产构建验收使用独立项目，不复用开发项目的凭据、元数据库或 Redis。先构建 `dbhub-backend:phase1-release` 与 `dbhub-frontend:phase1-release`，再运行：

```sh
./scripts/cloud/release.sh up -d --wait --wait-timeout 180
./scripts/cloud/release.sh run --rm --no-deps browser
./scripts/cloud/release.sh stop
```

生产 Compose 为根目录 `docker-compose.prod.yaml`；Cloud 额外合并 `release.compose.yaml` 提供本地依赖。验收状态保存在 `/workspace/dbhub-release-check`，数据库临时文件保存在 `${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}/release-data`，不能用作生产持久化路径。浏览器经 Nginx 访问真实 production 后端，额外覆盖角色权限、会话撤销、生产目标写入确认、62 秒长查询与超时原因指标。构建、后端替换核实、备份和回滚步骤见 [部署运维指南](../../docs/部署运维/部署运维指南.md)。

版本矩阵会使用当前开发后端容器执行 Go 竞态测试，基础 PostgreSQL/MySQL/Redis 必须已启动。目标版本逐个拉取、启动并清理，数据放在容器临时内存文件系统；默认删除本轮新拉取的镜像，保留运行前已有镜像，`--keep-images` 可保留本轮镜像。不要并行运行矩阵或重建后端；VFS 环境也应避免同时执行其他大量磁盘写入的构建。

元数据库保持使用基础 PostgreSQL 16；HTTP 夹具通过 `TEST_METADATA_DSN` 读取元库，而 `TEST_POSTGRES_DSN` 指向待测业务目标。目标表、采集故障和锁在业务实例上准备及清理，不把 PostgreSQL 14 业务库当作应用元库来执行迁移。矩阵报告同时保存实际工具链和元库版本。

报告在私有 `compatibility-artifacts/<UTC时间>/matrix.json`，记录真实补丁号、镜像摘要、测试名称、跳过与清理状态，以及后端 Go 实现和迁移 SQL 的独立指纹。PostgreSQL 各分支额外执行数据源检查、并发编辑、采集失败/互斥/取消的可靠性测试。完整有效报告可通过 `python3 scripts/cloud/compatibility.py --record-from /私有报告路径/matrix.json` 更新内嵌版本记录；部分报告、失败或任一实现/迁移指纹变化时拒绝更新。恢复运行会记录原报告摘要和复用项来源，不会掩盖原运行的失败。`.github/workflows/datasource-compatibility.yml` 提供手动触发的完整 CI 验证，不上传凭据文件。具体范围见 [数据源版本兼容](../../docs/开发指南/数据源版本兼容.md)。

版本提示的浏览器验收：`DBHUB_BROWSER_TEST_SCRIPT=/app/scripts/cloud/version_browser_test.py ./scripts/cloud/browser.sh`。验证真实数据库版本显示、修改连接后清除旧结果、过期响应处理及手机表单；MariaDB 警示使用界面契约样例，不代表真实产品兼容验证。

数据源异常与并发的定向回归：`./scripts/cloud/dev.sh exec -T backend go test -race -count=1 ./internal/server -run '^(TestDatasourceReliability|TestMetadataSyncReliability)$'`。测试会创建随机 Schema、临时账号与连接，在独立目标账号上设置 statement timeout 并对测试表加锁，验证采集失败和取消；不修改现有账号的权限。行为与原始结果见 [检查与采集可靠性](../../docs/开发指南/数据源检查与采集可靠性.md)。


JDBC 服务随 Cloud setup/start 启动，首次在 Docker 中安装 JDK 17、按锁定清单下载并核验驱动、编译 Java。已准备 PostgreSQL 42.7.7、MySQL 8.4.0、H2 2.4.240、openGauss 6.0.0-og。私有 runtime.env 自动生成服务 Token（至少 32 字符），配置内部 `DBHUB_JDBC_URL`，重复启动不轮换。应用构建和测试仍全部在 Docker 中执行。

JDBC 浏览器验收：`DBHUB_BROWSER_TEST_SCRIPT=/app/scripts/cloud/connector_browser_test.py ./scripts/cloud/browser.sh`。真实创建驱动、模板、H2/PG 实例，验证结构采集、修订绑定、SQL/键值/元数据视图与角色；测试清理自身对象，性能对照保存在私有 `connector-timing.json`。原有 SQL 与版本提示套件分别保留。不要在任一 Go 测试/矩阵执行期间重启或重建 backend；需要重启时先等待测试明确退出。

可选生产 JDBC 镜像使用 `services/jdbc-runner/Dockerfile`，Compose profile 为 `jdbc`，只运行已构建镜像，不在启动时安装 JDK。实际配置、增加驱动和验收范围见[连接器与 JDBC 自定义数据源验收](../../docs/开发指南/连接器与JDBC自定义数据源验收.md)。

JDBC 生产镜像的隔离验收：先按连接器指南提供公共 CA bundle 构建镜像，再执行 `DBHUB_JDBC_CHECK_IMAGE=dbhub-jdbc:<version> ./scripts/cloud/jdbc-image-check.sh`。脚本运行非 root、只读文件系统的镜像，重跑真实 JDBC 集成用例，并验证 PostgreSQL/MySQL 的 TLS 信任、主机名、只读及 MySQL 跨库外键。只创建和清理本轮随机命名的容器与证书，不部署生产；运行期间不要重启 Cloud JDBC 编排容器。


指定用户／用户组授权、目录与关联审计的验收使用 `DBHUB_AUDIENCE_BROWSER=1 python3 scripts/cloud/personal-browser-check.py`。浏览器会安装在临时容器 `dbhub-personal-browser`，数据库和目录夹具在 finally 中清理；完成全部浏览器检查后可删除此容器以回收空间。

本地 openGauss 演示使用 `python3 scripts/cloud/opengauss-demo-check.py`，仅串行启动一个固定摘要的社区 6.0.0 PG 模式单节点，限制 1.5 GiB 内存和 2 核 CPU，完成后清理容器、临时数据、凭据和本轮新增镜像。脚本不启动商业 GaussDB，也不替代主备／分布式及 TLS 验收；它要求既有 Cloud 后端和当前 JDBC 服务已启动。不要在其他 Go 测试执行期间重建 backend/JDBC。完整接口和边界见[数据源授权、目录与 openGauss 验收](../../docs/开发指南/数据源授权目录与openGauss验收.md)。

审计采用执行前持久化和数据库幂等补写。Cloud 队列保存在状态目录的 `audit/`，替换容器时保留该目录；生产 Compose 使用 UID 65532 可写的 `backend-audit` 命名卷。队列满或本地存储故障会暂停新的审计操作，`/api/ready` 返回 503；管理员页面可查看积压。覆盖、故障恢复和独立浏览器验收见[审计覆盖与可靠写入](../../docs/开发指南/审计覆盖与可靠写入.md)。


仪表盘第一阶段会在启动时运行 0028–0035 迁移，并为元数据库创建独立受限运行身份。启动数据库用户需拥有迁移对象、具备 `CREATEROLE` 和当前 Schema 授权能力；认证配置必须允许生成的登录角色连接。正常停止时回收本进程角色；备份到新环境建议使用 `--no-owner --no-acl`，不要搬运临时角色授权。具体升级规则见[仪表盘实施与验收](../../docs/开发指南/仪表盘第一阶段实施与验收.md)。

仪表盘独立浏览器验收复用现有生产运行镜像 `dbhub-backend:managed-reports` / `dbhub-frontend:managed-reports`，将当前构建只读挂载；无需重新构建大镜像层。宿主需已有 Python Playwright 与 `/usr/bin/chromium`。先构建，再执行：

```sh
./scripts/cloud/dev.sh exec -T backend go build -o /runtime/dbhub-dashboard-server ./cmd/server
./scripts/cloud/dev.sh exec -T frontend sh -ec 'npm run test:query; npm run test:report; npm run test:dashboard; npm run build'
python3 scripts/cloud/dashboard_acceptance.py
```

脚本不会向开发工作空间注入演示看板。每次创建随机私有元数据/业务 Schema、独立只读目标身份和两只受资源限制的临时运行容器，测试五张真实报表从开发到发布，包含业务名称与小数位预览、送审缺权检查、独立报表补权发布、审批业务差异，再检查普通查看者、手机首屏两张指标卡、页面并发、部分刷新、传输故障、过期清除、离线和切空间。结束后删除本轮容器、网络、卷、数据库对象与凭据文件；截图、`checks.json` 和后端日志保存在私有状态目录 `dashboard-artifacts/<随机编号>/`。不要把 `runtime.env` 或目标密码提交到仓库。最新证据见[易用性改进验收](../../docs/开发指南/仪表盘易用性改进与验收.md)。

## 生产可靠性验收

生产依赖故障、登录防护、严格 CSP、会话保留和完整报表/仪表盘恢复使用独立验收脚本，不依赖开发库数据。先按[生产修复验收](../../docs/开发指南/生产可靠性修复与验收.md)构建候选镜像，再运行：

```sh
export DBHUB_DASHBOARD_BACKEND_IMAGE=dbhub-backend:production-fix-20261018
export DBHUB_DASHBOARD_FRONTEND_IMAGE=dbhub-frontend:production-fix-20261018
DBHUB_DASHBOARD_USE_IMAGE=1 python3 scripts/cloud/dashboard_acceptance.py
python3 scripts/cloud/production_acceptance.py --duration 0
# 发布前按实际时钟运行30分钟；合成负载不代替真实业务容量认证
python3 scripts/cloud/production_acceptance.py --duration 1800 --clients 4
```

两个脚本复用宿主 Playwright/Chromium；浏览器路径可通过 `DBHUB_CHROMIUM_PATH` 指定。生产验收脚本创建独立元库、业务库、Redis、容器与卷，结束清理全部临时资源及凭据；`production-acceptance/<随机编号>/checks.json` 留作证据。CI 在 PR/main/手动触发时执行较短的完整故障恢复流程，30分钟负载在发布阶段单独执行。
