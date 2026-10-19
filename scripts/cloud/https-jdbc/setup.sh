#!/bin/sh
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
sh "$here/prepare.sh"
# Preserve the project installation/build/test contract, without stopping services.
sh "$here/compose.sh" run --rm --no-deps frontend npm ci --no-audit --no-fund
sh "$here/compose.sh" run --rm --no-deps -e DBHUB_JDBC_URL=http://jdbc:8090 backend sh -ec 'go test -p=4 ./...; go build -p=4 -o /runtime/dbhub-backend ./cmd/server'
sh "$here/compose.sh" run --rm --no-deps frontend sh -ec 'npm run test:query; npm run test:report; npm run test:dashboard; npm run test:automation; npm run build'
sh "$here/test.sh"
sh "$here/build.sh"
