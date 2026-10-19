#!/bin/sh
set -eu
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
sh "$here/prepare.sh"
sh "$here/build.sh"
export DBHUB_BACKUP_EXPECTED_HOURS=${DBHUB_BACKUP_EXPECTED_HOURS:-24}
sh "$here/compose.sh" config --quiet
sh "$here/compose.sh" up -d --no-deps --no-build jdbc-router
sh "$here/compose.sh" up -d --no-deps backend frontend
sh "$here/run-python.sh" health.py
sh "$here/run-python.sh" register.py
# Keep the latest main's managed backup startup contract.
sh "$here/../backup.sh"
