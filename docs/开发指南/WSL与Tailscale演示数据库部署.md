# WSL + Docker + Tailscale 演示数据库部署

本文在 Windows 本机通过 WSL 2 和 Docker 部署演示数据库，再让 Codex 云环境经 Tailscale 接入。默认只启动 PostgreSQL 16；MySQL 8.4、Redis 7 按需启用。包内只包含合成数据和数据库，不启动 DBHub 应用；DBHub 仍可运行在云端。

推荐组合为 Windows 11、WSL 2 Ubuntu 24.04、Docker Desktop 的 WSL 2 后端，以及 **Windows 版 Tailscale**。如果你直接在 WSL 中安装 Docker Engine，见第 8 节，避免混用不同系统的回环地址。

## 1. 连接方式和终端位置

```text
Codex 云环境 / DBHub 后端
    ↓ 经授权的 TCP CONNECT 代理与必要的转发
平台 Tailscale 设备
    ↓ 加密的 Tailscale 网络
你的 Windows Tailscale 设备（100.x.y.z）
    ↓ Tailscale Serve
Windows 127.0.0.1:15432
    ↓ Docker Desktop 发布端口
PostgreSQL 容器 :5432
```

本机不需要公网 IP、路由器端口映射或 Tailscale 子网路由。Serve 仅面向 Tailnet；不要为此开启面向公网的 Funnel。

| 操作 | 执行位置 |
| --- | --- |
| 安装或检查 WSL | Windows PowerShell |
| Docker Compose、初始化、数据库验证 | WSL Ubuntu Bash |
| Windows 版 Tailscale、Serve、检查 Windows 回环端口 | Windows PowerShell |
| 云环境 VPN、TCP 地址授权 | Codex 云环境设置 |
| 云端转发、DBHub 接入验收 | 新配置生效后的 Codex 云环境 |

不要把 WSL 的 `172.x` 地址、Docker 容器的内部地址当作云端连接地址。推荐使用 Windows Tailscale 的 `100.x` IPv4；本机 `localhost` 也不是云环境的 `localhost`。

## 2. 准备 Windows、WSL 和 Docker

在 Windows PowerShell 检查：

```powershell
wsl --version
wsl --list --verbose
```

Ubuntu 的 VERSION 应为 `2`。尚未安装时，按微软提示以管理员 PowerShell 执行，必要时重启：

```powershell
wsl --install -d Ubuntu-24.04
```

已安装其他 Ubuntu 版本可继续使用，不需要重新安装。仅在现有发行版为 WSL 1 时，用实际发行版名称执行 `wsl --set-version <发行版名称> 2`。

安装并启动 [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/)，启用 WSL 2 后端，在其 WSL Integration 设置中启用上述 Ubuntu 发行版。推荐至少给本机开发环境预留 4 GB 内存；默认 PostgreSQL 容器限制为 384 MB，开启 MySQL 后还需额外资源。

在 **WSL Bash** 执行：

```bash
docker version
docker compose version
python3 --version
git --version
```

Docker 应同时显示客户端和服务端，Compose 使用 v2，建议至少 2.20。不要在已经使用 Docker Desktop 的同一个 WSL 发行版中再启动一套 Docker Engine。

将项目放在 WSL 自身的 Linux 文件系统，例如 `~/projects/DBHub`，以保留文件权限并改善磁盘性能；不要优先放在 `/mnt/c`。

## 3. 获取配置并启动 PostgreSQL

如果使用仓库，在 **WSL Bash** 执行：

```bash
mkdir -p ~/projects
cd ~/projects
git clone https://github.com/xielforever/DBHub.git
cd DBHub/deploy/wsl-demo
```

已有仓库时，直接进入其 `deploy/wsl-demo` 目录，先妥善保存本地修改再更新仓库。若使用单独提供的 ZIP 包，将其解压到 WSL 的主目录，进入包含 `compose.yaml` 的 `dbhub-wsl-demo` 目录即可，后续命令相同。

初始化口令并启动：

```bash
bash prepare.sh
docker compose config -q
docker compose up -d --wait --wait-timeout 180
docker compose ps
bash verify.sh
```

`prepare.sh` 使用 Python 标准库生成 7 个独立随机口令，保存到本目录 `.env`，权限 `0600`，不会打印口令；重复运行复用已有配置，不会自动轮换。脚本还会设置容器初始化文件的读取/执行权限。`.env` 已被当前目录的 `.gitignore` 排除。不要运行不带 `-q` 的 `docker compose config` 并分享其输出，其中可能包含口令。

默认仅 PostgreSQL 启动。`verify.sh` 通过 TCP 和真实账号验证示例数据、只读账号拒绝写入、写入账号可在事务中更新并回滚、写入账号不能建表；未启用的 MySQL/Redis 显示 `SKIP`，不代表它们已验收。

| 服务 | 本机发布地址 | 数据库/账号 | 对应 `.env` 字段 |
| --- | --- | --- | --- |
| PostgreSQL | `127.0.0.1:15432` | `dbhub_demo` / `demo_reader` | `DEMO_PG_READER_PASSWORD` |
| PostgreSQL | 同上 | `dbhub_demo` / `demo_writer` | `DEMO_PG_WRITER_PASSWORD` |
| PostgreSQL 初始化管理员 | 同上 | `dbhub_demo` / `demo_admin` | `DEMO_PG_ADMIN_PASSWORD` |
| MySQL（可选） | `127.0.0.1:13306` | `dbhub_demo` / `demo_reader` | `DEMO_MYSQL_READER_PASSWORD` |
| MySQL（可选） | 同上 | `dbhub_demo` / `demo_writer` | `DEMO_MYSQL_WRITER_PASSWORD` |
| Redis（可选） | `127.0.0.1:16379` | 用户 `demo`，数据库 `0` | `DEMO_REDIS_PASSWORD` |

推荐 DBHub 首次使用 `demo_reader`；读写验收使用 `demo_writer`。两个 SQL 写入账号只有现有演示表的查询和增删改权限，没有建表、删表、用户管理权限。初始化管理员不用于日常接入。

SQL 数据库都包含 `inventory` 表及 3 条库存数据。可在 **WSL Bash** 执行只读查询：

```bash
docker compose exec postgres sh -c 'PGPASSWORD="$DEMO_READER_PASSWORD" psql -X -h127.0.0.1 -Udemo_reader -ddbhub_demo -c "SELECT id, sku, name, quantity FROM inventory ORDER BY id"'
```

需要填写数据库口令时，只在本机编辑器中查看 `.env` 的对应项，不要发送文件、口令或截图到聊天。

### 可选：启动 MySQL、Redis

在同一目录、同一个 **WSL Bash** 中执行：

```bash
# 同时启用两个可选服务；也可以只保留需要的一个 --profile。
docker compose --profile mysql --profile redis up -d --wait --wait-timeout 240
bash verify.sh
```

Redis 禁用默认匿名用户，`demo` 账号只能操作 `demo:*` 前缀的键，禁止危险管理命令。创建一条演示缓存：

```bash
docker compose exec redis sh -c 'REDISCLI_AUTH="$DEMO_REDIS_PASSWORD" redis-cli --user demo SET demo:inventory:KB-001 20'
```

容器采用命名卷持久化。PostgreSQL/MySQL 的初始化脚本只在**数据卷首次初始化**时执行；修改 `.env` 不会修改已有数据库用户的密码。需要保留数据时，应通过数据库命令轮换口令，再同步 `.env`；仅有可丢弃数据时才按第 9 节重建。

本包没有配置由客户端验证的数据库服务证书：PostgreSQL、Redis 默认为无原生 TLS，MySQL 镜像可能自动生成自签证书。本包仅用于经 Tailscale 私有链路访问的合成演示数据；验证数据库 TLS 时，须另外配置可信 CA、服务名及证书验证，不能靠关闭校验处理证书错误。镜像固定摘要用于复现，不会自动获取后续安全补丁。

## 4. 在 Windows 配置 Tailscale Serve

在 **Windows** 安装 [Tailscale](https://tailscale.com/download/windows)，登录你自己的 Tailnet，保持在线。重新打开 **Windows PowerShell**：

```powershell
tailscale status
tailscale ip -4
Test-NetConnection -ComputerName 127.0.0.1 -Port 15432
```

`TcpTestSucceeded` 必须为 `True`，说明 Windows 能访问 Docker 发布的回环端口。若不通，先检查 Docker Desktop 和容器状态，不要改成向所有网卡发布端口。

为 PostgreSQL 建立私有 TCP 转发：

```powershell
tailscale serve --bg --tcp=15432 tcp://127.0.0.1:15432
tailscale serve status
```

如 CLI 提示需要开启 Serve，按其提供的管理页面完成配置，再重试。若系统要求管理员权限，使用管理员 PowerShell 运行相同命令。

只有启用了对应数据库，才增加其转发：

```powershell
tailscale serve --bg --tcp=13306 tcp://127.0.0.1:13306
tailscale serve --bg --tcp=16379 tcp://127.0.0.1:16379
tailscale serve status
```

将 `tailscale ip -4` 返回的实际地址记为 `DEMO_TAILSCALE_IP`。例如它为 `100.101.102.103`，则云端授权目标是 `100.101.102.103:15432`；这只是填写示例，不是真实可用地址。

## 5. 把 Codex 云环境接入 Tailnet

在 Tailscale 管理后台准备供云环境加入网络的专用 Auth key，建议使用专用标签、适当的有效期；只有需要多次注册替换环境时才按实际需要开启 Reusable。密钥到期不会自动踢掉已经注册的设备，退出测试时还要检查设备列表。

在 **Codex 云环境设置中的 Tailscale VPN 配置入口**填写所需信息；具体字段以你看到的界面为准。Auth key 只填写在 VPN 配置入口，不写进仓库、数据库 `.env`、安装脚本或聊天。若 Tailnet 启用设备审批，批准对应的云端设备。

配置 Tailscale 访问策略，让云端设备访问演示设备的 TCP 15432。下面是使用专用云端标签的策略片段，须按实际 IP 修改，并**合并进现有策略**，不要覆盖整份文件：

```json
{
  "tagOwners": {
    "tag:codex-dev": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["tag:codex-dev"],
      "dst": ["100.101.102.103/32"],
      "ip": ["tcp:15432"]
    }
  ]
}
```

先允许管理员管理这个标签，再使用带该标签的 Auth key 或为云端设备分配该标签。MySQL、Redis 需要时再向 `ip` 增加 `tcp:13306`、`tcp:16379`。Tailscale 规则是叠加允许：如果保留现有“全员访问全部设备”的规则，仅添加上述窄规则不会收紧原有权限，应结合现有业务审查。

接着在 **Codex 云环境设置的 TCP 访问授权**中允许实际设备 IPv4，例如 `100.101.102.103/32`。这不是 HTTP 域名白名单；不需要为数据库额外开放一个公网域名，也无需添加整个 `100.64.0.0/10` 网段。

平台当前的 TCP 地址授权覆盖目标的所有 TCP 端口，端口范围由 Tailscale 策略继续限制。更新 TCP 授权需要使用新配置版本创建**替换环境**：保存并发布配置后，按产品界面使用该版本创建/替换运行环境，再验证实际服务。仅保存、仅发布或旧会话继续显示 `connected`，都不能证明新的 TCP 授权已生效。

云端 VPN 由平台代理管理，不要在 Codex 的执行容器中另装 Tailscale，也不要修改 `/etc/codex/network-policy.json` 尝试授权。

## 6. 交给 Codex 做云端接入验收

配置完成后，可以在聊天中提供以下**非敏感信息**：

```text
本机部署方式：WSL 2 + Docker Desktop + Windows Tailscale
Tailscale IPv4：实际的 100.x.y.z
PostgreSQL：15432 / dbhub_demo / demo_reader
MySQL：13306 / dbhub_demo / demo_reader（若已启用）
Redis：16379 / 数据库 0 / 用户 demo（若已启用）
```

数据库密码通过受控的应用配置、可直接读取的运行时变量或受限文件注入，不发到聊天。这里使用的是原始 TCP，不能依赖“仅在 HTTPS 代理请求中替换占位符”的代理密钥机制。

Codex 会先检查 VPN 配置和 TCP 授权，再通过平台 `proxy:8088` 的 HTTP CONNECT 建立必要的转发，进行真实数据库认证与查询。数据库原生驱动和 JDBC 不会仅凭 `HTTP_PROXY` 自动使用该通道。

DBHub 后端若运行在 Docker 中，转发必须放在后端可达的网络位置，并配置 DBHub 的目标地址白名单。不能把 Windows 的 `127.0.0.1:15432` 或云宿主机的回环地址直接当成任意后端容器都能访问的地址。原生数据库 TLS 一旦启用，还须保留原始服务名、SNI 和证书验证。

建议按以下顺序验收：

1. 云端真实客户端认证成功，查询版本和 `inventory` 数据；不能仅看监听端口或 `ping`。
2. DBHub 新建个人连接，完成连接测试、检查、结构采集与基础查询。
3. 只读账号写入失败，写入账号允许 DML、拒绝 DDL；Redis 拒绝非 `demo:*` 键及危险管理命令。
4. 验证共享模板与个人账号隔离，再做报表及仪表盘查询。

openGauss/商业 GaussDB 不在此基础包内，后续可单独准备匹配产品和版本的镜像及驱动；openGauss 验证不能直接作为商业 GaussDB 的实机验收。

## 7. 常见问题

| 现象 | 排查方法 |
| --- | --- |
| WSL 中找不到 `docker` 或连接不到服务端 | 先启动 Docker Desktop，再检查该发行版的 WSL Integration；不要同时启动另一套 Docker Engine |
| 提示端口已被占用 | 检查现有服务；可在 `.env` 增加 `DEMO_PG_PORT=25432` 后重建 PostgreSQL，并同步修改 Windows Serve 的本地目标、对外端口与 Tailscale 端口策略 |
| PowerShell 中找不到 `tailscale` | 确认安装的是 Windows 版，重新打开终端；必要时使用安装目录中的 `tailscale.exe` |
| WSL 内能访问，但 Windows 的端口检查失败 | 确认使用 Docker Desktop 模式；原生 WSL Docker Engine 应按第 8 节处理 |
| 修改密码后仍认证失败 | 初始化后的卷保存旧口令；不要靠反复运行 `prepare.sh` 或重启轮换密码 |
| SQL 初始化失败但容器还在 | 用 `docker compose logs --tail=80 postgres` 或 MySQL 日志定位原因；健康检查会等真实表查询成功。初次失败可能留下部分初始化的数据卷，不要对需要保留的数据直接执行 `down -v` |
| 云端 CONNECT 返回 403 | 检查 TCP `/32` 授权、目标 IP、运行环境是否使用新配置；HTTP 允许列表不授予数据库 TCP 权限 |
| 云端返回 `vpn_not_ready` 等 503 | 记录脱敏错误交由平台排查，不能仅据此判断本机数据库故障 |
| CONNECT 超时 | 检查本机是否在线/休眠、Serve、Tailscale 标签/策略、设备审批及本机安全软件 |
| 连上后提示账号或权限错误 | 检查数据库名、用户及口令；TCP 连通不代表应用认证成功 |
| Tailnet 名称无法解析 | 本平台未启用 MagicDNS/split DNS；本方案直接用已授权的 Tailscale IPv4 |

日志分享前先检查并脱敏，尤其是自己改过初始化脚本时；不要发送 `.env`、完整容器 inspect 或含密码的 DSN。

## 8. 如果使用的是 WSL 内原生 Docker Engine

此模式中 Docker Engine 和 Tailscale 都运行在**同一个 WSL 发行版**，Compose 及密码配置不变。不要同时使用 Windows Serve 去猜测 WSL 的 `localhost` 转发是否可用。

按 [Tailscale Linux 安装说明](https://tailscale.com/download/linux)安装到该 Ubuntu，确保 WSL 的 systemd 已启用，再在 **WSL Bash** 执行：

```bash
sudo systemctl enable --now tailscaled
sudo tailscale up
tailscale ip -4
sudo tailscale serve --bg --tcp=15432 tcp://127.0.0.1:15432
tailscale serve status
```

必要时先在 `/etc/wsl.conf` 的现有配置中合并 `[boot]` 下的 `systemd=true`，按微软文档重启该 WSL 环境；不要覆盖该文件的其他配置。此模式在云端授权的是 **WSL Tailscale 节点的 IPv4**，而非 Windows 节点的 IPv4。可选数据库在 WSL 中用相同 Serve 参数配置，并根据 CLI 提示使用 `sudo`。

WSL 关闭、Windows 休眠或重启后，服务需要重新检查；`restart: unless-stopped` 并不保证一个已停止的 WSL 或 Docker 服务会自行启动。参考：[WSL systemd](https://learn.microsoft.com/windows/wsl/systemd)。

## 9. 停止和清理

在 **Windows PowerShell** 只关闭本次创建的转发，不影响其他 Serve 配置：

```powershell
tailscale serve --tcp=15432 off
# 只有本次启用过这两条时才执行：
tailscale serve --tcp=13306 off
tailscale serve --tcp=16379 off
```

原生 WSL Docker Engine 模式应在 WSL 关闭对应的 Serve 转发，必要时加 `sudo`。

在配置目录的 **WSL Bash** 中停止全部演示服务，保留数据：

```bash
docker compose --profile mysql --profile redis down
```

确定这套演示数据可以全部丢弃时，删除本 Compose 项目的容器、网络和命名卷：

```bash
docker compose --profile mysql --profile redis down -v
```

只有卷删除完成且不再需要原口令时，才考虑删除本目录 `.env`。不要使用全局 `docker system prune` 或 `docker volume prune`，以免影响其他项目。长期不用时，移除云环境中的相应 TCP 授权，并在 Tailscale 后台撤销本次 Auth key、移除不再使用的云端设备。

## 10. 验证范围与官方参考

2026-10-19 已在 Linux Docker 28.4.0 / Compose 2.40.3 验证，记录见[演示环境验收结果](../acceptance/wsl-demo-2026-10-19.json)：

| 检查 | 结果 |
| --- | --- |
| 默认 PostgreSQL 启动；全部 profile 启动 | 通过 |
| 空卷初始化 PostgreSQL 16.15、MySQL 8.4.11、Redis 7.4.11 | 全部健康，首次启动进程重启次数均为 0 |
| SQL 示例数据、只读拒写、DML 事务回滚、DDL 拒绝 | 通过 |
| Redis 拒绝匿名访问、认证读写、键前缀和管理命令限制 | 通过 |
| 宿主机回环端口协议探测、仅绑定回环地址 | 通过 |
| 停止后保留卷重建、再次验收、最终清理临时资源 | 通过 |

Windows Docker Desktop 的端口映射、WSL 本机环境、Tailscale Serve、云端 VPN/TCP 链路仍须按本文在你的机器上逐层验收；本次验收时云环境未配置 VPN，完整链路尚未实测。

- [WSL 安装](https://learn.microsoft.com/windows/wsl/install)
- [Docker Desktop WSL 2 后端](https://docs.docker.com/desktop/features/wsl/)
- [Tailscale Serve](https://tailscale.com/kb/1242/tailscale-serve)
- [Tailscale 访问策略](https://tailscale.com/kb/1337/acl-syntax)
- [DBHub 正式部署入口](../../deploy/README.md)
