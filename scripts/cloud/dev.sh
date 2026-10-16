#!/bin/sh
# Local cloud configuration contains only newly generated development credentials.
set -eu
dbhub_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
export DBHUB_REPO_DIR="$dbhub_root"
export DBHUB_STATE_DIR="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
export DBHUB_BUILD_ROOT="${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}"
mkdir -p "$DBHUB_BUILD_ROOT/go" "$DBHUB_BUILD_ROOT/tmp"
chmod 700 "$DBHUB_BUILD_ROOT"
export DBHUB_UID="$(id -u)" DBHUB_GID="$(id -g)"
mkdir -p "$DBHUB_STATE_DIR/go-mod" "$DBHUB_STATE_DIR/npm-cache"
chmod 700 "$DBHUB_STATE_DIR"
if [ ! -f "$DBHUB_STATE_DIR/runtime.env" ]; then
 python3 - "$DBHUB_STATE_DIR/runtime.env" <<'PY'
import os,secrets,sys
password=secrets.token_hex(24)
values={'POSTGRES_USER':'dbhub','POSTGRES_PASSWORD':password,'POSTGRES_DB':'dbhub',
 'MYSQL_ROOT_PASSWORD':secrets.token_hex(24),'MYSQL_USER':'dbhub','MYSQL_PASSWORD':password,'MYSQL_DATABASE':'dbhub',
 'DATABASE_URL':f'postgres://dbhub:{password}@postgres:5432/dbhub?sslmode=disable',
 'APP_SECRET_KEY':secrets.token_hex(32),'APP_ADMIN_USERNAME':'admin','APP_ADMIN_PASSWORD':secrets.token_urlsafe(18),
 'REDIS_URL':'redis://redis:6379/0',
 'TEST_POSTGRES_DSN':f'postgres://dbhub:{password}@postgres:5432/dbhub?sslmode=disable',
 'TEST_MYSQL_DSN':f'dbhub:{password}@tcp(mysql:3306)/dbhub?parseTime=true',
 'TEST_REDIS_URL':'redis://redis:6379/15'}
fd=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f:
 for name,value in values.items(): f.write(f'{name}={value}\n')
PY
fi
export DBHUB_CA_FILE="${CODEX_PROXY_CERT:-${SSL_CERT_FILE:-/etc/ssl/certs/ca-certificates.crt}}"
dbhub_proxy="$(python3 - <<'PY'
import os,socket
from urllib.parse import urlsplit
u=urlsplit(os.environ.get('HTTPS_PROXY') or os.environ.get('https_proxy') or os.environ.get('HTTP_PROXY') or 'http://localhost:8080')
print(u.hostname+':'+socket.gethostbyname(u.hostname))
PY
)"
export DBHUB_PROXY_HOST="${dbhub_proxy%%:*}" DBHUB_PROXY_IP="${dbhub_proxy#*:}"
export DBHUB_NO_PROXY="localhost,127.0.0.1,frontend,backend,postgres,mysql,redis${NO_PROXY:+,$NO_PROXY}"
exec docker compose --project-name "${DBHUB_COMPOSE_PROJECT:-dbhub-cloud}" --project-directory "$dbhub_root" --env-file "$DBHUB_STATE_DIR/runtime.env" -f "${DBHUB_COMPOSE_FILE:-$dbhub_root/scripts/cloud/compose.yaml}" "$@"
