#!/bin/sh
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
export DBHUB_STATE_DIR="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
exec docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges --user "$(id -u):$(id -g)" --network "${DBHUB_COMPOSE_PROJECT:-dbhub-cloud}_default" -e PYTHONDONTWRITEBYTECODE=1 --mount "type=bind,src=$here,dst=/deployment,readonly" --mount "type=bind,src=$DBHUB_STATE_DIR,dst=/runtime" python:3.12-slim-bookworm@sha256:34386ef0cb081344d7ec1c103ba398e6e9f64e9ab3a1509accc92a4e24a07258 python "/deployment/$1"
