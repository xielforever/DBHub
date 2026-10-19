#!/bin/sh
# Internal wrapper: only startup selects the overlay; default dev.sh is unchanged.
set -eu
export PYTHONDONTWRITEBYTECODE=1
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
export DBHUB_REPO_DIR="$(CDPATH= cd -- "$here/../../.." && pwd)"
export DBHUB_STATE_DIR="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
export DBHUB_BUILD_ROOT="${DBHUB_BUILD_ROOT:-/tmp/dbhub-cloud-build}"
export DBHUB_UID="$(id -u)" DBHUB_GID="$(id -g)"
export DBHUB_CA_FILE="${CODEX_PROXY_CERT:-${SSL_CERT_FILE:-/etc/ssl/certs/ca-certificates.crt}}"
export HTTPS_PROXY="${HTTPS_PROXY:-${https_proxy:-}}" HTTP_PROXY="${HTTP_PROXY:-${http_proxy:-}}"
pair="$(python3 - <<'PY'
import os,socket
from urllib.parse import urlsplit
u=urlsplit(os.environ.get('HTTPS_PROXY') or os.environ.get('HTTP_PROXY') or 'http://localhost:8080')
print(u.hostname+':'+socket.gethostbyname(u.hostname))
PY
)"
export DBHUB_PROXY_HOST="${pair%%:*}" DBHUB_PROXY_IP="${pair#*:}"
export DBHUB_NO_PROXY="localhost,127.0.0.1,frontend,backend,postgres,mysql,redis,jdbc${NO_PROXY:+,$NO_PROXY}"
if [ "${1:-}" = --base ]; then
 export DBHUB_ORIGINAL_TARGET_ALLOWLIST="$(python3 "$here/state.py" original-allowlist)"
else
 export DBHUB_ROUTER_TARGET_ALLOWLIST="$(python3 "$here/state.py" allowlist)"
fi
exec python3 "$here/compose_exec.py" "$@"
