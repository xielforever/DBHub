#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
# 容器以不同 UID 读取这些公开脚本；仅口令文件需要 0600。
chmod 644 postgres-init.sql redis-start.sh
# 让 MySQL 作为子进程执行脚本，避免严格 shell 选项影响镜像入口。
chmod 755 mysql-init.sh
umask 077
python3 - <<'PY'
import os
import secrets
from pathlib import Path

path = Path('.env')
names = [
    'DEMO_PG_ADMIN_PASSWORD', 'DEMO_PG_READER_PASSWORD', 'DEMO_PG_WRITER_PASSWORD',
    'DEMO_MYSQL_ROOT_PASSWORD', 'DEMO_MYSQL_READER_PASSWORD', 'DEMO_MYSQL_WRITER_PASSWORD',
    'DEMO_REDIS_PASSWORD',
]
if path.exists() or path.is_symlink():
    if path.is_symlink() or not path.is_file():
        raise SystemExit('.env 必须是本目录内的普通文件。')
    present = {line.partition('=')[0] for line in path.read_text().splitlines()}
    if not set(names).issubset(present):
        raise SystemExit('.env 不完整；请检查文件，不会自动覆盖或轮换口令。')
    path.chmod(0o600)
    print('复用现有 .env，未更改口令。')
else:
    with open(path, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as output:
        output.write('# 仅本地演示；请勿提交或发送此文件。\n')
        for name in names:
            output.write(f'{name}={secrets.token_hex(24)}\n')
    print('已生成 .env（0600），未输出口令。')
PY
