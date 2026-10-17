#!/bin/sh
# 在独立容器中执行真实数据库压测；凭据只通过现有 Cloud 环境注入。
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
dbhubb_revision="$(git rev-parse HEAD)"
dbhubb_dirty=false
if [ -n "$(git status --porcelain)" ]; then dbhubb_dirty=true; fi
exec ./scripts/cloud/dev.sh run --rm --no-deps \
  -e "DBHUB_BENCH_REVISION=$dbhubb_revision" -e "DBHUB_BENCH_DIRTY=$dbhubb_dirty" \
  backend sh -ec 'exec go run -p=4 ./cmd/querybench "$@"' querybench "$@"
