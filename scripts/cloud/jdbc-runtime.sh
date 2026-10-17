#!/bin/sh
set -eu
export http_proxy="${HTTP_PROXY:-}" https_proxy="${HTTPS_PROXY:-}" no_proxy="${NO_PROXY:-}"
export SSL_CERT_FILE=/run/cloud-ca.pem
if ! command -v javac >/dev/null; then
 sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources
 apt-get -o Acquire::https::CaInfo=/run/cloud-ca.pem update -qq
 apt-get -o Acquire::https::CaInfo=/run/cloud-ca.pem install -y -qq --no-install-recommends openjdk-17-jdk-headless python3 ca-certificates >/dev/null
 rm -rf /var/lib/apt/lists/*
fi
mkdir -p /runtime/jdbc/lib /runtime/jdbc/classes
python3 /app/services/jdbc-runner/fetch-artifacts.py /app/services/jdbc-runner/artifacts.lock.json /runtime/jdbc/lib
javac -encoding UTF-8 -cp '/runtime/jdbc/lib/*' -d /runtime/jdbc/classes /app/services/jdbc-runner/src/DBHubJdbc.java
export JDBC_ARTIFACT_LOCK=/app/services/jdbc-runner/artifacts.lock.json JDBC_ARTIFACT_DIR=/runtime/jdbc/lib
exec setpriv --reuid="${DBHUB_RUNTIME_UID:-1000}" --regid="${DBHUB_RUNTIME_GID:-1000}" --clear-groups java -Xms32m -Xmx256m -cp '/runtime/jdbc/classes:/runtime/jdbc/lib/gson-2.13.2.jar' DBHubJdbc
