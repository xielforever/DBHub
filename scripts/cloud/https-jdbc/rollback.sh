#!/bin/sh
# Disable only the adapter. Application resources and private state are retained.
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
export DBHUB_HTTPS_JDBC_ENABLED=0
export DBHUB_BACKUP_EXPECTED_HOURS="${DBHUB_BACKUP_EXPECTED_HOURS:-24}"
sh "$here/compose.sh" --base up -d --no-deps backend
# Stop by exact project/service label, never remove or recreate dependencies.
python3 - <<'PY'
import os,subprocess
project=os.environ.get('DBHUB_COMPOSE_PROJECT','dbhub-cloud')
r=subprocess.run(['docker','ps','-q','--filter','label=com.docker.compose.project='+project,'--filter','label=com.docker.compose.service=jdbc-router'],capture_output=True,text=True,check=True)
ids=r.stdout.split()
if len(ids)>1:raise SystemExit('Ambiguous router identity; nothing stopped')
if ids:subprocess.run(['docker','stop',ids[0]],check=True,stdout=subprocess.DEVNULL)
PY
