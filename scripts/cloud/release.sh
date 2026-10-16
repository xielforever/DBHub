#!/bin/sh
# 独立本地验收项目；生产镜像使用生产配置，凭据与数据卷不复用开发项目。
set -eu
dbhubrelease_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
export DBHUB_STATE_DIR="${DBHUB_RELEASE_STATE_DIR:-/workspace/dbhub-release-check}"
export DBHUB_COMPOSE_PROJECT=dbhub-release-check
export DBHUB_COMPOSE_FILE="$dbhubrelease_root/docker-compose.prod.yaml"
export DBHUB_BACKEND_IMAGE="${DBHUB_RELEASE_BACKEND_IMAGE:-dbhub-backend:phase1-release}"
export DBHUB_FRONTEND_IMAGE="${DBHUB_RELEASE_FRONTEND_IMAGE:-dbhub-frontend:phase1-release}"
export DBHUB_BUILD_ROOT="${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}"
mkdir -p "$DBHUB_BUILD_ROOT/release-data/postgres" "$DBHUB_BUILD_ROOT/release-data/mysql" "$DBHUB_BUILD_ROOT/release-data/redis"
exec "$dbhubrelease_root/scripts/cloud/dev.sh" -f "$dbhubrelease_root/scripts/cloud/release.compose.yaml" "$@"
