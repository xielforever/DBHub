#!/bin/sh
set -eu
dbhub_backup_health_dir=${DBHUB_BACKUP_DIR:-/var/lib/dbhub/backups}
[ -f "$dbhub_backup_health_dir/.last-success" ] || exit 1
[ "$(cat "$dbhub_backup_health_dir/.last-result" 2>/dev/null)" = succeeded ] || exit 1
dbhub_backup_success=$(cat "$dbhub_backup_health_dir/.last-success")
case "$dbhub_backup_success" in ''|*[!0-9]*) exit 1;; esac
dbhub_backup_age=$(( $(date +%s)-dbhub_backup_success ))
[ "$dbhub_backup_age" -ge 0 ] && [ "$dbhub_backup_age" -lt "$(( ${DBHUB_BACKUP_INTERVAL_SECONDS:-86400}*2 ))" ]
