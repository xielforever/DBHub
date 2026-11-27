# SunMao 流水线发布

DBHub 的常规发布通道：Gitee 镜像仓检出 → 集群内 kaniko 构建前后端镜像并推送 SWR → Helm 发布到 CCE 集群 `xyc` 的 `dbhub` 命名空间 → HTTP 就绪探测。

本目录只保存流水线定义与说明；真实凭据、ELB ID、目标白名单不进入版本库。

## 前置条件

1. **Gitee 镜像仓**：`https://gitee.com/Forget-Me-Not/DBHub.git`，由 `.github/workflows/sync-to-gitee.yml` 在每次 GitHub push 后强制同步，commit SHA 与 GitHub 一致。SunMao 项目绑定该仓库与 `main` 分支。
2. **基础镜像**：SWR `dx_x2era` 中需存在 `golang:1.26.9-bookworm`、`debian:bookworm-slim`、`node:24.12.0-bullseye-slim`、`nginx:1.30.6-alpine`（由集群侧一次性镜像化）。
3. **SunMao 凭证**：
   - `gitee`：Gitee Token，用于检出。
   - `swr-auth`：类型 `swr_auth`，`registry=swr.cn-east-2.myhuaweicloud.com`，`username/password` 为 SWR AK/SK。
   - `dbhub-kubeconfig`：类型 `kubeconfig`，内容为可管理 `dbhub` 命名空间的 kubeconfig，用于 `helm_deploy` 跨命名空间发布。
4. **集群资源**：`dbhub` 命名空间已存在 `dbhub-runtime`、`default-secret`、`dbhub-datastore-ca`、`dbhub-postgres`、`dbhub-redis`。
5. **Gitee WebHook**：URL 指向 SunMao `/api/webhooks/gitee`，口令与 `SUNMAO_WEBHOOK_SECRET` 一致，事件选 Push。

## 使用方式

1. 在 SunMao 项目中新建流水线，使用 YAML 模式粘贴 [pipeline.yaml](pipeline.yaml)。
2. 将 `env.ELB_ID` 替换为现有 CCE ELB ID，`env.TARGET_ALLOWLIST` 替换为数据源目标地址白名单。
3. 先手动触发一次（`DEPLOY` 保持默认 `false`）：验证检出、两个镜像构建推送、`helm template` 渲染，不修改集群。
4. 正式发布时手动触发并传 `DEPLOY=true`：执行 `helm upgrade --install --wait --atomic`，随后做 HTTP 就绪探测。
5. webhook 触发时 `DEPLOY` 取默认值 `false`，只做构建与渲染校验，避免每次 push 直接发布。

## 参数

| 名称 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `DEPLOY` | bool | `false` | `true` 执行 Helm 发布；`false` 只构建并渲染校验 |

镜像 tag 为 `${COMMIT_SHA_SHORT}-${BUILD_NUMBER}`，不使用 `latest`；values 中不写 `digest`，Chart 自动使用 tag。

## 实现约束（改动流水线时注意）

- **变量只替换一次**：SunMao 的 `${VAR}` 替换不递归，`env` 里不能再定义引用内置变量的值。因此 tag 直接写在 `destination` 与 `values_inline` 中，不定义 `TAG`。
- **不使用 `health_check` 步骤**：该步骤用 SunMao 自身 ServiceAccount 访问集群 API，而该账号对 `dbhub` 命名空间无权限。就绪由 `helm_deploy` 的 `wait`/`atomic` 与三个 `http_request` 覆盖。
- **环境值不入库**：`ELB_ID`、`TARGET_ALLOWLIST` 在平台侧编辑；`nodePort` 等类型化值只能放 `values_inline`，不能用 `set`（仅支持 `--set-string`）。

## 验收

- 构建日志：Gitee 检出、两个镜像推送成功、`helm template` 通过。
- 发布后：`dbhub-dbhub-backend`、`dbhub-dbhub-frontend` 各 1 副本就绪，镜像为本次 tag，Helm revision 递增。
- HTTP：前端 `/healthz`、后端 `/api/ready`、`/api/health` 均 200；经 ELB 打开登录页并完成登录、退出。
- 迁移：在流水线外用只读 kubectl 核对 `schema_migrations` 最大值与本次提交的迁移文件一致。

## 回滚

新建只含 `helm_deploy action: rollback` 的流水线（`revision` 留空回滚到上一版），或重新发布上一版镜像 tag。数据库迁移不会随 Helm 回滚，回退前须确认旧镜像与当前 Schema 兼容。

## 能力边界

SunMao 流程不覆盖：镜像摘要（digest）固定、流水线内迁移比对（`shell` 无 kubeconfig 挂载）、`helm lint`、登录会话自动验收。需要这些能力时使用 `scripts/deploy/xyc_release.py`（本机 Docker 构建、digest 校验、迁移逐条比对），两者并存：SunMao 为常规通道，脚本为应急与离线校验。
