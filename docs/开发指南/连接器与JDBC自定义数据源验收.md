# 连接器与 JDBC 自定义数据源验收

2026-10-17，完成连接器抽象、统一命名和 JDBC 自定义数据源首版。具体证据见[验收记录](../acceptance/connector-jdbc-2026-10-17.json)，性能样本见[端到端对照](../benchmarks/connector-jdbc-2026-10-17.json)。原生版本矩阵与 JDBC 产品验证分别记录。

本文记录 0021 阶段历史验收。当前权限与创建流程以 [0022 共享数据源与个人连接](共享数据源与个人连接验收.md)为准，驱动协议和既有版本证据保留。

## 交付内容

导航和标题统一为概览、数据源、数据工作台、数据目录、报表、仪表盘、变更工单、连接代理等，使用同一份名称定义。保留现有路由、API 和权限码。

数据工作台按服务端能力打开：MySQL/PostgreSQL 显示 SQL 编辑器和结果，仍只有运行、格式化两个主要操作；Redis 显示键扫描、五类值读取、TTL、删除和新建字符串；仅支持元数据的 JDBC 实例显示库、Schema、表、字段。只读角色不显示写入入口，后端仍独立检查权限。

查询、事务、预览、导出、报表、工单、连接检查和结构采集使用统一连接器运行时；原生 Go 驱动保持默认。新增迁移 0021，包含不可变驱动版本、模板修订、实例绑定与元数据可用状态。

## 当前接入范围

| 接入 | 驱动 / 实测目标 | 本轮功能 |
| --- | --- | --- |
| MySQL 原生 | 现有 Go 驱动；5.7 / 8.0 / 8.4 | 保留原有功能与版本回归 |
| PostgreSQL 原生 | pgx；14–18 | 保留原有功能与版本回归 |
| Redis 原生 | go-redis；6.2 / 7.2 / 7.4 / 8.0 | 保留五类值、TTL、原子写入；新增基础键值界面 |
| PostgreSQL JDBC | 42.7.7；Cloud PostgreSQL 16.15 | 检查、SQL、参数、只读事务、提交/回滚、元数据与配置库采集 |
| MySQL JDBC | 8.4.0；Cloud MySQL 8.4.11 | 检查、SQL、参数、服务端只读、元数据与配置库采集 |
| H2 JDBC | 2.4.240；真实内存数据库 | 检查、结构浏览与采集；公开 SQL 未启用 |
| 其他部署制品 | 管理员加入锁定清单后发布通用网络模板 | 通用元数据浏览；SQL、事务、完整采集关闭，逐产品验证后扩展 |

H2 用于新增产品的真实验证和演示，内存库随 Java 服务重启消失，不是持久化业务数据库方案。Oracle 风格模板只有配置契约单测，没有加载 Oracle 驱动或实测 Oracle 服务；版本 UI 中 MariaDB 仍为契约样例。不能由这些样例推导产品已兼容。

JDBC 使用当前实例配置的逻辑库，不承诺枚举整个服务器的所有库。复合主键按 KEY_SEQ 保留列顺序，MySQL 跨库外键保留实际引用库。DDL、行数和空间统计尚未实现，数据目录明确显示不支持；主键、索引、外键依驱动实际元数据返回。未经完整范围验证的未知产品不允许采集清理旧快照。

## 在界面中创建自定义数据源

1. 管理员进入“系统设置 → 驱动管理”，选择部署中已安装、已校验的制品并登记。
2. 在“数据源模板”选择驱动，填写模板标识、名称、产品、URL 规则和连接字段。MySQL/PostgreSQL 可选择对应的已有方言；其他产品保留“仅结构浏览”。
3. 发布模板。再次修改使用“新修订”，不会改变已有实例的修订绑定。
4. 进入“数据源 → 新建数据源 → 自定义 JDBC”，选择修订，填写字段与独立凭据，测试连接并保存。
5. 点击“打开”。支持完整采集的模板提供“采集结构”，快照在数据目录查看。

首次发布直接可用，没有额外草稿审批状态。驱动登记与模板发布仅 admin；developer 沿用自身数据源编辑权限，readonly 只读。引用中的版本无法删除；停用阻止后续开启的新会话，已取得配置的在途请求仍可能完成。

已知驱动当前使用固定 URL 规则：

```text
jdbc:postgresql://{host}:{port}/{database}
jdbc:mysql://{host}:{port}/{database}
jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1
```

其他驱动可使用包含 `//{host}:{port}` 的通用网络 URL 模板，最多 20 个声明字段。字段必须与占位符一致且必填；port 为整数，其余为字符串，可设默认值。端点和参数规范化后生成 URL，凭据、执行脚本和任意连接属性不在实例中透传。特殊 URL 和认证方式需补产品适配。

## 引入额外驱动

修改 `services/jdbc-runner/artifacts.lock.json`，固定新的制品 ID、驱动类、版本、文件名、可信 HTTPS Maven 来源与 SHA-256，核对许可证、JDK 17 兼容性及所需依赖。依赖也登记独立清单项，通过 `dependencies` 引用其 ID。重建服务后，启动会先校验全部文件，再为每个驱动加载指定依赖。

第一版不提供浏览器上传 JAR。未知驱动只需清单和通用模板即可尝试检查与结构浏览；不因此自动获得 SQL 方言或完整采集。上线某产品前应真实验证 URL、认证、Catalog/Schema、字段类型与权限不足时的行为。

## 部署与启动

Cloud 执行 `./scripts/cloud/setup.sh` 和 `./scripts/cloud/start.sh`。安装、构建和测试全部在 Docker 中；脚本启动 postgres、mysql、redis、jdbc、backend、frontend 六个服务。首次下载使用平台代理/CA 和锁定校验值；配置、Token、JAR 与日志保存在仓库外私有目录。

生产 JDBC 为可选 profile，先构建不可变镜像再运行：

```sh
docker build --secret id=proxy_ca,src="${JDBC_BUILD_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}" -f services/jdbc-runner/Dockerfile -t dbhub-jdbc:<version> services/jdbc-runner
# 配置 DBHUB_JDBC_IMAGE、DBHUB_JDBC_URL=http://jdbc:8090、DBHUB_JDBC_TOKEN
# 再沿用生产的其他私有配置：
docker compose -f docker-compose.prod.yaml --profile jdbc up -d
```

迁移 0021 为向前扩展，旧应用不能识别 JDBC 实例；回退时应恢复匹配的应用与元数据库备份。

私有 CA 可通过只读挂载 JVM trust store 配置；PostgreSQL 使用 DefaultJavaSSLFactory，MySQL 使用 JVM trust store。CA-only JKS 可沿用 JDK 公共默认完整性口令，不能把数据源口令或含私钥的 keystore 密码写入 JAVA_TOOL_OPTIONS，因为 JVM 会打印该变量。

Token 至少 32 字符，由密钥管理注入 Go 和 Java，不能放到前端或提交到仓库。JDBC 端口只在服务网络开放；单机内部使用 Bearer 鉴权，跨主机部署提供 TLS 或服务网格保护。镜像以非 root 用户、只读根文件系统运行。Debian HTTPS 安装需要通过 `proxy_ca` BuildKit secret 提供公共 CA bundle；Cloud 使用平台 CA，可将 `JDBC_BUILD_CA_BUNDLE` 指向 `CODEX_PROXY_CERT`。APT 降权用户只读访问 CA，该文件不会复制进运行镜像。代理网络同时传递 Docker build proxy args，不关闭 TLS 校验。

目标连接首版支持直连。PostgreSQL/MySQL 已在隔离实例验证 TLS：可信 CA 和正确主机名可连接并完成只读查询，不受信任 CA、主机名错误均拒绝；代理、SOCKS 和 SSH 暂不支持，配置时明确拒绝。未知产品和 H2 演示不提供 TLS 开关。Go 原生传输能力不会自动授予 JDBC。

## 异常、并发与值语义

Go 持久化请求认领和发送前登记；Java 会话内再按执行键防重，不自动重发 SQL。12 个并发相同写入编号只产生一次目标更新。丢失已提交写入响应时回执为 `unknown`，重复提交返回冲突；通过原回执核实，不重跑。

显式事务固定目标会话，验证了会话内读取未提交值、回滚和提交；显式 40 秒预算下真实 31 秒查询可完成，不被内部默认 30 秒提前终止。只读使用 AST 与目标只读事务。编辑草稿改变模板、参数或目标身份后要求重输密码；保存检查与配置版本绑定。模板修订和驱动校验值固定，引用版本不能原地覆盖。

JDBC 整数、小数以无损文本返回，二进制显式 base64，SQL NULL 与 JSON 文本 null 分开，日期时间保留原始语义。结果有行数、字节及单字段上限，超限明确提示。当前不是流式前端接口。

结构采集继续整轮事务发布、跨实例同源互斥、配置变更检测。提交确认丢失时新增 `unknown`，提示刷新数据目录核实。`TestMetadataCommitAcknowledgementLossIsUnknown` 通过真实提交后模拟确认丢失验证该分类及锁释放。

## 复现与性能

```sh
./scripts/cloud/dev.sh exec -T backend go test -race -p=4 -count=1 ./...
./scripts/cloud/dev.sh exec -T backend go vet -p=4 ./...
./scripts/cloud/dev.sh exec -T frontend sh -ec 'npm run test:query; npm run build'
DBHUB_BROWSER_TEST_SCRIPT=/app/scripts/cloud/connector_browser_test.py ./scripts/cloud/browser.sh
DBHUB_BROWSER_TEST_SCRIPT=/app/scripts/cloud/version_browser_test.py ./scripts/cloud/browser.sh
./scripts/cloud/browser.sh
python3 scripts/cloud/compatibility.py
# 使用已构建镜像，自动建立并清理隔离 TLS 数据库和证书：
DBHUB_JDBC_CHECK_IMAGE=dbhub-jdbc:<version> ./scripts/cloud/jdbc-image-check.sh
```

镜像验收包括 11 个 JDBC 子场景与 12 项私有协议/TLS 检查；运行用户为非 root，根文件系统只读。报告保存在私有 jdbc-image-artifacts，每轮清理测试容器、证书和凭据文件；该脚本不部署生产。

测试和矩阵执行期间不要重启 backend；镜像验收期间也不要重启其用于编排的 Cloud JDBC 容器。版本矩阵逐目标执行、清理临时容器与本轮新镜像；Docker VFS 环境不要同时进行大镜像构建。

浏览器性能对照使用同一 PostgreSQL、三类查询、每类每种实现预热 2 次后串行 20 次，均按请求新建目标连接。测量包含 HTTP 鉴权、回执持久化、目标驱动/RPC 与 JSON 解码。当前 JDBC 延迟高于原生路径，因此已有 MySQL/PostgreSQL 默认保持原生接入。

| 负载 | 原生 P50 / P95（ms） | JDBC P50 / P95（ms） |
| --- | --- | --- |
| 短查询 | 7.8 / 8.2 | 12.9 / 15.3 |
| 1000 行 | 8.5 / 11.2 | 18.8 / 23.8 |
| 聚合 | 9.0 / 9.7 | 14.4 / 19.8 |

JDBC RPC 已启用 TCP_NODELAY，避免小响应的传输延迟累积。原始精度和统计方法保存在对照 JSON。

这只是开发环境端到端样本，没有完整覆盖冷启动、并发吞吐、P99、CPU/内存容量或生产 SLA。连接池、流式传输、新产品方言、特殊认证、上传制品和分布式配额留待后续实际需求。


## 界面验收截图

截图只包含验收生成的数据：[SQL 桌面](../acceptance/images/workbench-sql-desktop.png)、[结构浏览手机](../acceptance/images/workbench-metadata-mobile.png)、[Redis 键值手机](../acceptance/images/workbench-redis-mobile.png)。
