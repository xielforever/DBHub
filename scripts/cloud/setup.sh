#!/bin/sh
# Install, build and test entirely in Docker, using the cloud proxy and CA.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
# Explicit opt-in is validated before any Docker or state mutation.
python3 scripts/cloud/https-jdbc/config.py
if [ "${DBHUB_HTTPS_JDBC_ENABLED:-0}" = 1 ]; then
 exec sh "scripts/cloud/https-jdbc/setup.sh" "$@"
fi
command -v docker >/dev/null
docker compose version >/dev/null
scripts/cloud/dev.sh stop backend frontend
scripts/cloud/dev.sh pull
scripts/cloud/dev.sh up -d --wait --wait-timeout 240 postgres mysql redis jdbc
scripts/cloud/dev.sh run --rm --no-deps frontend npm ci --no-audit --no-fund
scripts/cloud/dev.sh run --rm --no-deps backend sh -ec 'go test -p=4 ./...; go build -p=4 -o /runtime/dbhub-backend ./cmd/server'
scripts/cloud/dev.sh run --rm --no-deps frontend sh -ec 'npm run test:query; npm run test:report; npm run test:dashboard; npm run test:automation; npm run build'
