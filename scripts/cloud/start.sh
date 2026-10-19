#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
# Explicit opt-in is validated before any Docker or state mutation.
python3 scripts/cloud/https-jdbc/config.py
if [ "${DBHUB_HTTPS_JDBC_ENABLED:-0}" = 1 ]; then
 exec sh "scripts/cloud/https-jdbc/start.sh" "$@"
fi
scripts/cloud/dev.sh up -d --wait --wait-timeout 240 jdbc
export DBHUB_BACKUP_EXPECTED_HOURS=${DBHUB_BACKUP_EXPECTED_HOURS:-24}
scripts/cloud/dev.sh up -d backend frontend
python3 - <<'PY'
import time,urllib.request
for _ in range(60):
 try:
  with urllib.request.urlopen('http://127.0.0.1:8080/api/ready',timeout=2) as response: assert response.status==200
  with urllib.request.urlopen('http://127.0.0.1:5173/',timeout=2) as response: assert response.status==200
  print('DBHub services ready')
  break
 except Exception: time.sleep(1)
else: raise SystemExit('DBHub startup timed out; inspect scripts/cloud/dev.sh logs backend frontend')
PY
scripts/cloud/backup.sh
