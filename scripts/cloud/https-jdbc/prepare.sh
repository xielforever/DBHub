#!/bin/sh
set -eu
export PYTHONDONTWRITEBYTECODE=1
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
python3 "$here/config.py"
export DBHUB_STATE_DIR="${DBHUB_STATE_DIR:-/workspace/dbhub-cloud}"
# Validate before dev.sh is allowed to create any private runtime state.
python3 "$here/state.py" check-root
sh "$here/../dev.sh" config --quiet
python3 "$here/state.py" router-copy
sh "$here/router-credentials.sh"
python3 "$here/infrastructure.py"
