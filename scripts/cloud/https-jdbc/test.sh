#!/bin/sh
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
docker run --rm --network=none --read-only --tmpfs /tmp:rw,exec,nosuid,size=16m --cap-drop=ALL --security-opt=no-new-privileges --user "$(id -u):$(id -g)" -e PYTHONDONTWRITEBYTECODE=1 --mount "type=bind,src=$here/..,dst=/review,readonly" -w /review/https-jdbc python:3.12-slim-bookworm@sha256:34386ef0cb081344d7ec1c103ba398e6e9f64e9ab3a1509accc92a4e24a07258 sh -ec 'python -m unittest -v test_router test_deployment; for f in *.sh ../start.sh ../setup.sh; do sh -n "$f"; done'
