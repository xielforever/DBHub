# WSL 演示数据库

本目录提供WSL演示数据源配置，环境与运行约定见[开发指南](../../docs/开发指南.md)。默认只启动PostgreSQL；MySQL、Redis为可选profile。本目录不部署DBHub应用。

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
