#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

# 验证客户端使用真实密码和 TCP，不依赖容器本机的 socket 信任。
docker compose exec -T postgres sh -eu -c '
  export PGPASSWORD="$DEMO_READER_PASSWORD"
  demo_count="$(psql -X -h127.0.0.1 -Udemo_reader -ddbhub_demo -vON_ERROR_STOP=1 -tAc "SELECT count(*) FROM inventory")"
  test "$demo_count" -ge 3
  if psql -X -h127.0.0.1 -Udemo_reader -ddbhub_demo -vON_ERROR_STOP=1 -c "BEGIN; UPDATE inventory SET quantity=quantity WHERE id=1; ROLLBACK" >/dev/null 2>&1; then
    echo "FAIL: PostgreSQL 只读账号可以写入" >&2; exit 1
  fi
  export PGPASSWORD="$DEMO_WRITER_PASSWORD"
  psql -X -h127.0.0.1 -Udemo_writer -ddbhub_demo -vON_ERROR_STOP=1 -c "BEGIN; UPDATE inventory SET quantity=quantity WHERE id=1; ROLLBACK" >/dev/null
  if psql -X -h127.0.0.1 -Udemo_writer -ddbhub_demo -vON_ERROR_STOP=1 -c "BEGIN; CREATE TABLE public.verify_forbidden(id integer); ROLLBACK" >/dev/null 2>&1; then
    echo "FAIL: PostgreSQL 写入账号可以建表" >&2; exit 1
  fi
  echo "PASS: PostgreSQL 数据、只读限制、事务写入、DDL 限制"
'

demo_running="$(docker compose --profile mysql --profile redis ps --status running --services)"
if printf '%s\n' "$demo_running" | grep -qx mysql; then
  docker compose exec -T mysql bash -euc '
    export MYSQL_PWD="$DEMO_READER_PASSWORD"
    demo_count="$(mysql --protocol=TCP -h127.0.0.1 -udemo_reader -Ddbhub_demo -Nse "SELECT COUNT(*) FROM inventory")"
    test "$demo_count" -ge 3
    if mysql --protocol=TCP -h127.0.0.1 -udemo_reader -Ddbhub_demo -e "START TRANSACTION; UPDATE inventory SET quantity=quantity WHERE id=1; ROLLBACK" >/dev/null 2>&1; then
      echo "FAIL: MySQL 只读账号可以写入" >&2; exit 1
    fi
    export MYSQL_PWD="$DEMO_WRITER_PASSWORD"
    mysql --protocol=TCP -h127.0.0.1 -udemo_writer -Ddbhub_demo -e "START TRANSACTION; UPDATE inventory SET quantity=quantity WHERE id=1; ROLLBACK" >/dev/null
    if mysql --protocol=TCP -h127.0.0.1 -udemo_writer -Ddbhub_demo -e "CREATE TABLE verify_forbidden(id integer)" >/dev/null 2>&1; then
      echo "FAIL: MySQL 写入账号可以建表" >&2; exit 1
    fi
    echo "PASS: MySQL 数据、只读限制、事务写入、DDL 限制"
  '
else
  printf '%s\n' 'SKIP: MySQL 未启动'
fi

if printf '%s\n' "$demo_running" | grep -qx redis; then
  docker compose exec -T redis sh -eu -c '
    test "$(redis-cli ping 2>&1 | head -c 6)" = NOAUTH
    export REDISCLI_AUTH="$DEMO_REDIS_PASSWORD"
    demo_key="demo:verify:$$"
    test "$(redis-cli --user demo SET "$demo_key" passed EX 60)" = OK
    test "$(redis-cli --user demo GET "$demo_key")" = passed
    test "$(redis-cli --user demo DEL "$demo_key")" = 1
    test "$(redis-cli --user demo ACL DRYRUN demo FLUSHALL 2>&1 | head -c 6)" = NOPERM
    test "$(redis-cli --user demo SET forbidden:verify denied 2>&1 | head -c 6)" = NOPERM
    echo "PASS: Redis 认证、读写、键前缀限制、管理命令限制"
  '
else
  printf '%s\n' 'SKIP: Redis 未启动'
fi
