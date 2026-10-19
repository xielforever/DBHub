# 部署方式

本机 WSL 演示数据库及云端 Tailscale 接入见 [WSL 与 Tailscale 演示数据库部署](../docs/开发指南/WSL与Tailscale演示数据库部署.md)，可运行配置位于 [`wsl-demo/`](wsl-demo/)。该入口用于准备被 DBHub 接入的演示数据源。

DBHub 提供四种部署入口，使用同一后端、静态前端与生产环境变量。PostgreSQL 与 Redis 由运维准备；JDBC、定时备份按需启用。镜像需要自行构建或从已发布的内部仓库获取，示例仓库地址须替换。

| 方式 | 入口 | 适用情况 |
| --- | --- | --- |
| Docker Compose | 根目录 `docker-compose.prod.yaml`，可叠加 `docker-compose.ops.yaml` | 单机/虚拟机，优先推荐 |
| Docker CLI | `python3 deploy/docker/manage.py` | 只安装 Docker Engine，不使用 Compose |
| Helm / Kubernetes | `deploy/helm/dbhub` | 已有集群、Ingress、StorageClass 与 Secret 管理 |
| 源码 / systemd | `sh deploy/source/build.sh`、`deploy/source/*.service` | Linux 主机、本地编译、企业主机运维 |

第一版均以**单后端实例**为支持范围。工作台活动事务仍在进程内，Helm 禁止后端多副本，更新使用 Recreate；前端可独立扩容。部署入口可用不等于生产准入完成，见[生产上线评估](../docs/开发指南/生产上线评估与回归测试.md)及[运维指南](../docs/部署运维/部署运维指南.md)。

## 通用准备

1. 使用干净的发布提交。准备 PostgreSQL 元库及 Redis，网络/TLS 可达；元库启动身份需创建/认证受限运行角色及执行内嵌迁移，详见运维指南。
2. 复制 [dbhub.env.example](dbhub.env.example) 到仓库外的私有文件，权限 `0600`，填入强主密钥、管理员口令、DSN 与明确的目标地址白名单。主密钥在升级/恢复时保留。不要提交真实凭据。
3. 环境变量采用字面 `KEY=value`，不使用 `export`、引号或命令替换；URL 中口令按 URL 规则编码。Compose 的 env 文件有插值语法，包含 `$` 的值须按 Compose 文档转义；不要把不同工具的文件解析语法混用。源码服务由 systemd 读取环境文件，Docker CLI 从不执行文件内容。
4. 为审计安排可靠持久存储，支持同步写入；配置 HTTPS 入口和可信代理。审计卷、元库、应用主密钥、驱动制品及业务目标库分别纳入恢复方案。

构建容器镜像（仓库根目录）：

```sh
export DBHUB_RELEASE=$(git rev-parse --short=12 HEAD)
export DBHUB_BACKEND_IMAGE=registry.example.com/dbhub/backend:$DBHUB_RELEASE
export DBHUB_FRONTEND_IMAGE=registry.example.com/dbhub/frontend:$DBHUB_RELEASE
export DBHUB_JDBC_IMAGE=registry.example.com/dbhub/jdbc:$DBHUB_RELEASE
export DBHUB_BACKUP_IMAGE=registry.example.com/dbhub/backup:$DBHUB_RELEASE
docker build -f backend/Dockerfile -t "$DBHUB_BACKEND_IMAGE" backend
docker build -f frontend/Dockerfile -t "$DBHUB_FRONTEND_IMAGE" frontend
docker build -f scripts/ops/Dockerfile -t "$DBHUB_BACKUP_IMAGE" scripts/ops
# 可选 JDBC；proxy_ca 应为完整公共信任 bundle，可包含企业 CA，保持 TLS 验证。
docker build --secret id=proxy_ca,src=/etc/ssl/certs/ca-certificates.crt \
  -f services/jdbc-runner/Dockerfile -t "$DBHUB_JDBC_IMAGE" services/jdbc-runner
```

云代理环境构建前端时补充 `--secret id=proxy_ca,src="$CODEX_PROXY_CERT"`；JDBC 构建同样使用对应完整 CA bundle。跨主机/Kubernetes 运行前将选定镜像推送至集群可访问的仓库；生产建议记录并使用镜像摘要。公共证书及平台代理不应打入运行镜像。

## Docker Compose

将实际镜像引用填入私有 env 文件，运行：

```sh
docker compose --env-file /etc/dbhub/dbhub.env -f docker-compose.prod.yaml config -q
docker compose --env-file /etc/dbhub/dbhub.env -f docker-compose.prod.yaml up -d --wait --wait-timeout 240
```

默认仅发布前端 `127.0.0.1:8088`，交给 HTTPS 网关。JDBC 在 env 文件设置 `DBHUB_JDBC_URL=http://jdbc:8090` 和相同共享令牌后，加 `--profile jdbc`。自动备份按运维指南准备 UID 65532 可读的 libpq 私有目录，设置 `DBHUB_BACKUP_CONFIG_DIR`，叠加 `-f docker-compose.ops.yaml --profile operations`。每日备份默认保留 14 天；启用后设置 `DBHUB_BACKUP_EXPECTED_HOURS=24`。

升级前备份并排空活动查询；更换镜像后等待就绪，再验收登录、权限与正式查询。`docker compose down` 保留命名卷；不要在升级/回退时加 `-v`。

## 独立 Docker

需要 Docker Engine、Python 3，无需 Compose。配置文件包含实际镜像引用及同样的生产变量：

```sh
python3 deploy/docker/manage.py up --name dbhub --env-file /etc/dbhub/dbhub.env
python3 deploy/docker/manage.py status --name dbhub
# 更新前排空请求，再停止并保留审计卷，修改配置中的镜像引用，然后重新 up。
python3 deploy/docker/manage.py down --name dbhub
```

入口创建带所有权标签的专用网络、持久审计卷、后端及前端容器，等待镜像健康检查通过。已有同名部署须受控停机再更新；不会覆盖其他容器/卷。启动失败回收本轮已启动容器，审计卷保留。`down` 保留审计卷，有其他容器使用网络时也保留网络。

`up --jdbc` 增加 JDBC，要求配置 `DBHUB_JDBC_IMAGE` 与至少 32 字符的令牌；该容器只接收自身令牌。`--bind`、`--port` 可配置前端发布地址，默认 `127.0.0.1:8088`。`--ca-dir` 挂载公共 CA 目录到后端/JDBC 的 `/etc/dbhub-ca`；PostgreSQL DSN/驱动属性指定对应证书文件。Redis/HTTPS 使用私有 CA 时可在 env 文件设置 `SSL_CERT_FILE=/etc/dbhub-ca/ca-bundle.pem`，该文件须包含完整信任链；Java 信任配置按具体驱动要求处理。

备份可使用同一备份镜像独立运行（依赖已准备的私有 libpq 目录）：

```sh
docker volume create dbhub-metadata-backups
docker run -d --name dbhub-metadata-backup --restart unless-stopped --init \
  --read-only --cap-drop=ALL --security-opt no-new-privileges \
  --network dbhub-network \
  --tmpfs /tmp:uid=65532,gid=65532,size=16m \
  --tmpfs /var/lib/postgresql/data:uid=65532,gid=65532,size=1m \
  --mount type=bind,src=/etc/dbhub/backup,dst=/run/dbhub-backup,readonly \
  --mount type=volume,src=dbhub-metadata-backups,dst=/var/lib/dbhub/backups \
  -e PGSERVICE=dbhub -e PGSERVICEFILE=/run/dbhub-backup/pg_service.conf \
  -e PGPASSFILE=/run/dbhub-backup/pgpass "$DBHUB_BACKUP_IMAGE"
```

备份目录、文件由 UID 65532 持有，目录 `0700`、文件 `0600`；备份服务独立维护，不属于 `manage.py down` 的停止范围。

## Helm / Kubernetes

要求 Helm 3、Kubernetes 1.27+，并已有可用的外部元库/Redis、持久存储与 Ingress Controller。Chart 使用外部 Secret，不将口令保存到 values 或 Helm 发布历史中。先准备命名空间，再从私有文件创建运行 Secret：

```sh
kubectl create namespace dbhub
kubectl -n dbhub create secret generic dbhub-runtime --from-env-file=/secure/dbhub/credentials.env
# credentials.env：APP_SECRET_KEY、APP_ADMIN_PASSWORD、DATABASE_URL、REDIS_URL；启用 JDBC 时增加 DBHUB_JDBC_TOKEN。
helm lint deploy/helm/dbhub -f deploy/helm/values.example.yaml
helm upgrade --install dbhub deploy/helm/dbhub --namespace dbhub \
  -f deploy/helm/values.example.yaml --wait --timeout 10m
```

先复制并修改 [values.example.yaml](helm/values.example.yaml) 的实际镜像、域名、白名单及 Secret 名称；上述路径也应替换为维护的发布配置。TLS Secret 须提前创建，或由所用证书管理器生成。Ingress 超时需覆盖 180 秒，并关闭代理请求重放。未启用 Ingress 时通过内部 Service 接入受控网关，`kubectl port-forward` 仅用于验收。

默认创建保留的审计 PVC，也可通过 `backend.audit.existingClaim` 复用；审计文件位于 PVC 的 `audit/` 子目录。挂载到父目录，让 UID 65532 在 CSI 的 `root:fsGroup` 卷上创建自身目录。使用 `fsGroupChangePolicy: OnRootMismatch`，防止重挂载递归修改私有审计目录的 0700 权限。存储应支持该所有权策略、同步写入与 RWO。`storageClass: null` 使用集群默认，`""` 用于预绑定静态卷。已有审计数据迁入时保留目录层次与 UID，验证未落库事件恢复。

可选 JDBC 通过 ClusterIP 连接，UID 10001，临时目录受限；自定义 CA 可使用 `jdbc.extraVolumes/extraVolumeMounts`。后端同样提供额外挂载，PostgreSQL DSN 可引用所挂证书；Redis/AI 的私有 CA 可通过运行 Secret 的 `SSL_CERT_FILE` 指向完整公共信任 bundle。

启用 `backup.enabled` 后需指定备份镜像和独立的 libpq Secret，其中含 `pg_service.conf`、`pgpass`，必要时附 CA 文件。配置在非 root init 容器中复制成 UID 65532 的 `0600` 文件，避免 Kubernetes Secret 默认所有者/权限导致 libpq 拒读。文件路径位于 `/run/dbhub-backup`。CronJob 禁止重叠、保留至少两份完成归档，备份 PVC 内为 `backups/`。计划按集群控制器时区执行，默认示例为每日 02:00；统一采用 UTC。启用后设置 `config.DBHUB_BACKUP_EXPECTED_HOURS: "24"`，并将备份复制到异地。

`helm uninstall` 保留本 Chart 创建的审计/备份 PVC；删除命名空间仍会删除命名空间内的 PVC。Secret 更新需受控重启后端/JDBC。Helm 回滚不回滚数据库迁移，必须先验证旧镜像与当前 Schema 的兼容性或恢复独立元库。当前不提供后端 HPA/多副本支持。

## 源码 / systemd

适用于 Linux + systemd。构建主机需 Go 1.26.9、C 编译器/开发头文件、Node 24.12.0、npm；Go SQL 解析器启用 CGO。运行主机需匹配的 libc、CA、时区数据和受维护的 Nginx。可选 JDBC 需 JDK 17 构建、JRE 17 运行及 Python 3 下载/核验锁定驱动；备份需与元库版本匹配的 PostgreSQL 客户端、`flock`、`timeout`、`sha256sum`。

```sh
export DBHUB_RELEASE=$(git rev-parse --short=12 HEAD)
sh deploy/source/build.sh all .dbhub/build-$DBHUB_RELEASE
# 可选 JDBC，同一个提交的 staging 目录；不在服务启动时下载驱动。
sh deploy/source/build.sh jdbc .dbhub/build-$DBHUB_RELEASE
```

构建只写 staging，不安装或重启服务；拒绝不属于本工具的非空目录或混用不同提交。默认拒绝脏工作区；本地验证可使用 `DBHUB_ALLOW_DIRTY_BUILD=1`，其状态写入 `SOURCE.txt`，不作为正式发布制品。

由运维安装到版本目录，首次部署示例：

```sh
sudo useradd --system --user-group --home-dir /var/lib/dbhub --shell /usr/sbin/nologin dbhub
sudo install -d -m 0755 /opt/dbhub/releases/$DBHUB_RELEASE
sudo cp -R .dbhub/build-$DBHUB_RELEASE/. /opt/dbhub/releases/$DBHUB_RELEASE/
sudo ln -s /opt/dbhub/releases/$DBHUB_RELEASE /opt/dbhub/current
sudo install -d -m 0750 -o root -g dbhub /etc/dbhub
# 在 /etc/dbhub/dbhub.env 安全填写生产配置，root 持有、0600。
sudo install -m 0644 deploy/source/dbhub.service /etc/systemd/system/
sudo install -m 0644 deploy/source/nginx.conf /etc/nginx/conf.d/dbhub.conf
sudo install -m 0644 frontend/security-headers.conf /etc/nginx/dbhub-security-headers.conf
sudo systemctl daemon-reload
sudo systemctl enable --now dbhub
sudo nginx -t
sudo systemctl reload nginx
```

后端由 unit 固定监听 `127.0.0.1:8080`，Nginx 在 `127.0.0.1:8088`，由 HTTPS 网关接入。审计在 `/var/lib/dbhub/audit`，systemd 自动建立服务状态目录。升级先备份、排空，将新版本安装到新目录后原子切换 `current` 并重启；保留旧版本与相应配置/密钥，按运维指南回退。

JDBC 可选创建独立的 `dbhub-jdbc` 系统用户，安装 `dbhub-jdbc.service`，仅在 `/etc/dbhub/jdbc.env` 提供共享令牌。后端的配置中填入同一令牌及 `DBHUB_JDBC_URL=http://127.0.0.1:8090`。JDBC 仅监听回环地址，独立 UID 避免与后端共享进程凭据。

备份安装 `dbhub-backup.service`、`dbhub-backup.timer`，在 `/etc/dbhub/backup` 准备由 `dbhub` 用户持有的 libpq 配置（目录 `0700`、文件 `0600`），再执行 `systemctl enable --now dbhub-backup.timer`。失败及超期仍需外部告警监控，不用本机归档替代异地备份。

## 部署回归

```sh
python3 -m pip install -r deploy/requirements-check.txt
python3 -m unittest discover -s scripts/deploy -p 'test_*.py'
python3 scripts/deploy/check.py --schema
```

检查 Helm 的基础、JDBC/TLS/备份、复用 PVC 和镜像摘要四种配置；拒绝多副本、缺少 Secret、将密钥写入 ConfigMap 等无效配置；按 Kubernetes 1.32 Schema 校验并核对生产 Compose。Docker、源码与模拟 CSI 权限的实际运行验证见 `scripts/deploy/runtime_check.py`，需要 Playwright 依赖、候选镜像和已构建源码包。它创建独立合成数据库、Redis 和临时容器，验证登录、真实 SQL、受控重启、审计卷保留、非 root Nginx、源码二进制及回环绑定，结束销毁本轮资源。

模板/Schema 验证和 Docker 中的 Pod 权限模拟不等于真实集群安装验收；具体 CSI、Ingress、Secret 管理、网络及生产容量须在目标集群验收。

本批实际结果、制品指纹与未验范围见[部署方式验收记录](../docs/acceptance/deployment-modes-2026-10-18.json)。
