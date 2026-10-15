#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
# Browser dependencies stay in an ephemeral container; no host installation.
if docker container inspect dbhub-browser-check >/dev/null 2>&1; then
 docker rm dbhub-browser-check >/dev/null
fi
dbhubbrowser_status=0
scripts/cloud/dev.sh run --name dbhub-browser-check --no-deps -u 0 backend sh -ec '
 export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"
 export PIP_CERT=/run/cloud-ca.pem SSL_CERT_FILE=/run/cloud-ca.pem
 sed -i "s|http://deb.debian.org|https://deb.debian.org|g" /etc/apt/sources.list.d/debian.sources
 apt-get -o Acquire::https::CaInfo=/run/cloud-ca.pem update -qq
 apt-get -o Acquire::https::CaInfo=/run/cloud-ca.pem install -y -qq --no-install-recommends chromium python3-venv fonts-noto-cjk >/dev/null
 python3 -m venv /tmp/browser-venv
 /tmp/browser-venv/bin/pip -q install playwright==1.56.0
 /tmp/browser-venv/bin/python /app/scripts/cloud/browser_test.py
' || dbhubbrowser_status=$?
docker rm -f dbhub-browser-check >/dev/null 2>&1 || true
exit "$dbhubbrowser_status"
