# SunMao 流水线发布

DBHub 的常规发布通道：Gitee 镜像仓检出 → 集群内 kaniko 构建前后端镜像并推送 SWR → Helm 发布到 CCE 集群 `xyc` 的 `dbhub` 命名空间 → HTTP 就绪探测。

本目录保存流水线定义与说明。真实的 CCE 静态配置（ELB ID、节点端口、目标白名单、Nacos 地址/namespace）已固化在 [pipeline.yaml](pipeline.yaml) 中，这些不是凭据；运行凭据仍只存在于集群 Secret 与 SunMao 凭证里。

## 前置条件

1. **Gitee 镜像仓**：`https://gitee.com/Forget-Me-Not/DBHub.git`，由 `.github/workflows/sync-to-gitee.yml` 在每次 GitHub push 后强制同步，commit SHA 与 GitHub 一致。SunMao 项目绑定该仓库与 `main` 分支。
2. **基础镜像**：SWR `dx_x2era` 中需存在 `golang:1.26.9-bookworm`、`debian:bookworm-slim`、`node:24.12.0-bullseye-slim`、`nginx:1.30.6-alpine`（已一次性镜像化）。
3. **SunMao 凭证**：
   - `gitee`：Gitee Token，用于检出。
   - `swr-auth`：类型 `swr_auth`，`registry=swr.cn-east-2.myhuaweicloud.com`，`username/password` 为 SWR AK/SK。
   - `dbhub-kubeconfig`：类型 `kubeconfig`，内容为可管理 `dbhub` 命名空间的 kubeconfig，用于 `helm_deploy` 跨命名空间发布。
4. **集群资源**：`dbhub` 命名空间已存在 `dbhub-runtime`、`default-secret`、`dbhub-datastore-ca`、`dbhub-report44-fixture-ca`。元数据库与缓存使用 `infra` 命名空间的共享实例：`infra-core-postgres`（库 `dbhub`、角色 `dbhub_owner`、TLS `sslmode=verify-ca`，CA 以 `infra-ca.crt` 挂在 `dbhub-datastore-ca`）与 `infra-core-redis`（逻辑库 5），连接串由 Nacos 配置 `dbhub.properties` 下发；`dbhub` 命名空间原有的 `dbhub-postgres`、`dbhub-redis` 及其 PVC、凭据 Secret 已全部删除，本环境不再保留本地数据存储，也无旧库回退路径。
5. **Nacos**：`dbhub` namespace 下的 `dbhub.properties` 已存在；`dbhub-runtime` 需包含 `DBHUB_NACOS_USERNAME`/`DBHUB_NACOS_PASSWORD`。
6. **Gitee WebHook**：URL 指向 SunMao `/api/webhooks/gitee`，口令与 `SUNMAO_WEBHOOK_SECRET` 一致，事件选 Push。

## 使用方式

1. 在 SunMao 项目中编辑流水线，YAML 模式粘贴 [pipeline.yaml](pipeline.yaml)。ELB ID 与白名单已写入，无需再替换。
2. 先手动触发一次（`DEPLOY` 保持默认 `false`）：验证检出、两个镜像构建推送、`helm template` 渲染，不修改集群。
3. 正式发布时手动触发并传 `DEPLOY=true`：执行 `helm upgrade --install --wait --atomic`，随后做 HTTP 就绪探测。
4. webhook 触发时 `DEPLOY` 取默认值 `false`，只做构建与渲染校验，避免每次 push 直接发布。

## 参数

| 名称 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `DEPLOY` | bool | `false` | `true` 执行 Helm 发布；`false` 只构建并渲染校验 |

镜像 tag 为 `${TAG}`（即 `${COMMIT_SHA_SHORT}-${BUILD_NUMBER}`），不使用 `latest`；values 中不写 `digest`，Chart 自动使用 tag。

## 平台能力与实现约定

当前 SunMao（`20261127-8321fa6`）已支持：

- **env 多轮嵌套展开**：`env.TAG` 引用 `${COMMIT_SHA_SHORT}-${BUILD_NUMBER}`，后续步骤统一使用 `${TAG}`。
- **kaniko 资源拆分**：`resources.cpu/memory` 为 limits，`requests_cpu/requests_memory` 控制调度请求；本流水线显式设置后端 `1 CPU / 2Gi`、前端 `500m / 1Gi`，避免在分配率高的节点 Pending。
- **helm_deploy 工作区**：本地 Chart 路径会自动挂载 `/workspace`；本文件仍显式保留 `workspace: true`，两种方式都能工作。
- **跨命名空间探测**：`health_check`/`shell` 支持 `kubeconfig_credential`；本流水线仍用 `http_request` 探测服务 DNS，不依赖集群 API。
- **单流水线互斥**：同一流水线不会并发执行，可避免 Helm 锁冲突。

其他约定：

- `render-check`（`action: template`）**不挂** `kubeconfig_credential`，只做本地渲染；显式指定 `helm_version: "3.17"`。
- `release` 使用 `kubeconfig_credential: ${KUBECONFIG_CRED}` 跨命名空间发布，同样指定 `helm_version: "3.17"`。
- `backend.extraVolumes.datastore-ca` 必须是 `projected` 卷，同时挂载 `dbhub-datastore-ca` 与 `dbhub-report44-fixture-ca`（`report44-fixture-ca.pem`），不得退化为单一 configMap。
- `nodePort` 等类型化值只能放在 `values_inline`（`set` 仅支持 `--set-string`）。
- **编辑注意**：通过 SunMao 编排器表单保存 `helm_deploy` 步骤时，实测会丢失 `workspace` 标志（表现为 `helm template` 报 `repo deploy not found`）。优先直接修改 YAML；若使用表单，保存后回到“公共字段”确认“共享工作区”仍为开启。

## 验收

- 构建日志：Gitee 检出、两个镜像推送成功、`helm template` 通过。
- 发布后：`dbhub-dbhub-backend`、`dbhub-dbhub-frontend` 各 1 副本就绪，镜像为本次 tag，Helm revision 递增。
- HTTP：前端 `/healthz`、后端 `/api/ready`、`/api/health` 均 200；经 ELB 打开登录页并完成登录、退出。
- 迁移：在流水线外用只读 kubectl 核对 `schema_migrations` 最大值与本次提交的迁移文件一致；后端启动日志应包含 `Nacos 配置源已启用`。

## 回滚

使用 `dbhub-cce-rollback` 流水线（只含 `helm_deploy action: rollback`，`revision` 留空回滚上一版），或重新发布上一版镜像 tag。数据库迁移不会随 Helm 回滚，回退前须确认旧镜像与当前 Schema 兼容。

## 能力边界

SunMao 流程不覆盖：镜像摘要（digest）固定、`helm lint`、登录会话自动验收。迁移比对现在可借助 `shell` + `kubeconfig_credential` 实现，但尚未纳入本流水线。需要这些能力时使用 `scripts/deploy/xyc_release.py`（本机 Docker 构建、digest 校验、迁移逐条比对），两者并存：SunMao 为常规通道，脚本为应急与离线校验。
