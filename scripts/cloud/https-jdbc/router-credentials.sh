#!/bin/sh
# Short-lived root helper has no network, no directory mounts and only CHOWN/DAC.
# The long-running router always runs as 65532:65532.
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
state="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
exec docker run --rm --network=none --read-only --user 0:0 --cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_OVERRIDE --security-opt=no-new-privileges --mount "type=bind,src=$here/credential_owner.py,dst=/check.py,readonly" --mount "type=bind,src=$state/https-jdbc.credentials.json,dst=/run/source.json,readonly" --mount "type=bind,src=$state/https-jdbc-router.credentials.json,dst=/run/router.json" python:3.12-slim-bookworm@sha256:34386ef0cb081344d7ec1c103ba398e6e9f64e9ab3a1509accc92a4e24a07258 python /check.py
