#!/usr/bin/env bash
# systemd invokes this for the same invocation even after a timeout or kill.
set -euo pipefail
[[ ${INVOCATION_ID:-} =~ ^[a-f0-9]{32}$ ]] || exit 0
base=${DBHUB_LOCAL_BACKUP_DIR:-$HOME/.local/share/dbhub/backups}
[[ $base == /* && ! -L $base ]] || exit 2
marker="$base/.paused-$INVOCATION_ID"
[[ -f $marker && ! -L $marker ]] || exit 0
read -r backend <"$marker"
[[ $backend =~ ^dbhub-[a-zA-Z0-9_.-]+$ ]] || exit 2
if [[ $(docker inspect -f '{{.State.Paused}}' "$backend") == true ]]; then docker unpause "$backend" >/dev/null; fi
rm -f -- "$marker"
