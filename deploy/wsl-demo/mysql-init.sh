#!/usr/bin/env bash
set -euo pipefail
# prepare.sh 生成十六进制口令；限制输入格式以避免插入未转义的 SQL 字符。
for demo_value in "$DEMO_READER_PASSWORD" "$DEMO_WRITER_PASSWORD"; do
  if [[ ! "$demo_value" =~ ^[0-9a-f]{48}$ ]]; then
    printf '%s\n' 'MySQL 演示口令格式错误，请使用 prepare.sh 生成的配置。' >&2
    exit 1
  fi
done
MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --default-character-set=utf8mb4 --protocol=socket -uroot "$MYSQL_DATABASE" <<SQL
CREATE TABLE inventory (
    id integer PRIMARY KEY AUTO_INCREMENT,
    sku varchar(64) NOT NULL UNIQUE,
    name varchar(100) NOT NULL,
    quantity integer NOT NULL CHECK (quantity >= 0),
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO inventory (sku, name, quantity) VALUES
    ('KB-001', '键盘', 20), ('MS-001', '鼠标', 50), ('MN-001', '显示器', 12);
CREATE USER 'demo_reader'@'%' IDENTIFIED BY '$DEMO_READER_PASSWORD';
CREATE USER 'demo_writer'@'%' IDENTIFIED BY '$DEMO_WRITER_PASSWORD';
GRANT SELECT ON dbhub_demo.* TO 'demo_reader'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON dbhub_demo.* TO 'demo_writer'@'%';
SQL
