#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
# Browser dependencies stay in an ephemeral container; no host installation.
if docker container inspect dbhub-browser-check >/dev/null 2>&1; then
 docker rm dbhub-browser-check >/dev/null
fi
dbhubbrowser_status=0
scripts/cloud/dev.sh run --name dbhub-browser-check --no-deps browser || dbhubbrowser_status=$?
docker rm -f dbhub-browser-check >/dev/null 2>&1 || true
exit "$dbhubbrowser_status"
