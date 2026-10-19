# WSL 演示数据库

完整操作步骤见 [WSL 与 Tailscale 演示数据库部署](../../docs/开发指南/WSL与Tailscale演示数据库部署.md)。默认只启动 PostgreSQL；MySQL、Redis 为可选 profile。本目录不部署 DBHub 应用。

以下在 WSL Bash 的本目录执行：

```bash
bash prepare.sh
docker compose config -q
docker compose up -d --wait --wait-timeout 180
bash verify.sh

# 可选：
docker compose --profile mysql --profile redis up -d --wait --wait-timeout 240
bash verify.sh

# 停止并保留数据：
docker compose --profile mysql --profile redis down
```

本地端口仅绑定 `127.0.0.1`；默认 PostgreSQL 15432、MySQL 13306、Redis 16379。口令由 `prepare.sh` 生成在本地 `.env`，不要提交或分享。SQL 日常接入使用 `demo_reader` 或 `demo_writer`，Redis 使用 `demo`。
