#!/bin/sh
# Build an immutable staging bundle; never install/restart a live service.
set -eu
dbhub_repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
dbhub_target=${1:-all}
dbhub_output=${2:-"$dbhub_repo/.dbhub/release"}
case "$dbhub_target" in all|backend|frontend|jdbc) ;; *) echo 'Usage: build.sh [all|backend|frontend|jdbc] [STAGING_DIRECTORY]' >&2; exit 2;; esac
dbhub_dirty=$(git -C "$dbhub_repo" status --porcelain)
if [ -n "$dbhub_dirty" ] && [ "${DBHUB_ALLOW_DIRTY_BUILD:-0}" != 1 ]; then
 echo 'Use a clean committed checkout, or DBHUB_ALLOW_DIRTY_BUILD=1 for local validation only' >&2; exit 1
fi
mkdir -p "$dbhub_output"
dbhub_output=$(CDPATH= cd -- "$dbhub_output" && pwd)
case "$dbhub_output/" in "$dbhub_repo/"|"$dbhub_repo/backend/"*|"$dbhub_repo/frontend/"*|"$dbhub_repo/deploy/"*|//) echo 'Use a separate staging directory' >&2; exit 2;; esac
dbhub_revision=$(git -C "$dbhub_repo" rev-parse HEAD)
if [ -e "$dbhub_output/.dbhub-build" ]; then
 [ "$(cat "$dbhub_output/.dbhub-build")" = "$dbhub_revision" ] || { echo 'Use a new staging directory for another revision' >&2; exit 1; }
else
 [ -z "$(ls -A "$dbhub_output")" ] || { echo 'Refusing a nonempty, unowned staging directory' >&2; exit 1; }
 printf '%s\n' "$dbhub_revision" >"$dbhub_output/.dbhub-build"
fi
mkdir -p "$dbhub_output/bin" "$dbhub_output/config" "$dbhub_output/ops"
if [ "$dbhub_target" = all ] || [ "$dbhub_target" = backend ]; then
 (
  cd "$dbhub_repo/backend"
  export CGO_ENABLED=1 GOFLAGS=-mod=vendor GOTOOLCHAIN=local
  go build -p=4 -trimpath -o "$dbhub_output/bin/dbhub" ./cmd/server
  go build -p=4 -trimpath -o "$dbhub_output/bin/healthcheck" ./cmd/healthcheck
 )
fi
if [ "$dbhub_target" = all ] || [ "$dbhub_target" = frontend ]; then
 (cd "$dbhub_repo/frontend"; npm ci --no-audit --no-fund; npm run build)
 # The marker above proves this is our staging directory at the same revision.
 rm -rf -- "$dbhub_output/frontend"
 mkdir -p "$dbhub_output/frontend"
 cp -R "$dbhub_repo/frontend/dist/." "$dbhub_output/frontend/"
fi
if [ "$dbhub_target" = jdbc ]; then
 mkdir -p "$dbhub_output/jdbc/lib" "$dbhub_output/jdbc/classes"
 python3 "$dbhub_repo/services/jdbc-runner/fetch-artifacts.py" "$dbhub_repo/services/jdbc-runner/artifacts.lock.json" "$dbhub_output/jdbc/lib"
 javac --release 17 -encoding UTF-8 -cp "$dbhub_output/jdbc/lib/*" -d "$dbhub_output/jdbc/classes" "$dbhub_repo/services/jdbc-runner/src/DBHubJdbc.java"
 cp "$dbhub_repo/services/jdbc-runner/artifacts.lock.json" "$dbhub_output/jdbc/"
fi
cp "$dbhub_repo/deploy/dbhub.env.example" "$dbhub_output/config/"
cp "$dbhub_repo/deploy/source/"*.service "$dbhub_repo/deploy/source/"*.timer "$dbhub_repo/deploy/source/nginx.conf" "$dbhub_output/config/"
cp "$dbhub_repo/frontend/security-headers.conf" "$dbhub_output/config/dbhub-security-headers.conf"
cp "$dbhub_repo/scripts/ops/"*.sh "$dbhub_repo/scripts/ops/verify-metadata.sql" "$dbhub_output/ops/"
# Bundles contain public binaries/assets/examples only. Keep live secrets in /etc.
# Normalize modes even when a previous download used a private shell umask.
chmod 755 "$dbhub_output"
for dbhub_component in bin config ops frontend jdbc; do
 if [ -d "$dbhub_output/$dbhub_component" ]; then
  find "$dbhub_output/$dbhub_component" -type d -exec chmod 755 {} +
  find "$dbhub_output/$dbhub_component" -type f -exec chmod 644 {} +
 fi
done
for dbhub_binary in dbhub healthcheck; do
 [ ! -f "$dbhub_output/bin/$dbhub_binary" ] || chmod 755 "$dbhub_output/bin/$dbhub_binary"
done
printf 'SOURCE_COMMIT=%s\nWORKTREE_DIRTY=%s\n' "$dbhub_revision" "${dbhub_dirty:+yes}" >"$dbhub_output/SOURCE.txt"
printf 'Built %s in %s\n' "$dbhub_target" "$dbhub_output"
