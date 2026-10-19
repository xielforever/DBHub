#!/bin/sh
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
# Explicit context excludes credentials even if private state is misplaced.
tar -C "$here" -cf - Dockerfile router.py | docker build --network=none -t dbhub-https-jdbc-router:v1 -
