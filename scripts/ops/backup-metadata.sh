#!/bin/sh
# libpq credentials come from a protected service/pass file, never command args.
set -eu
if [ "$#" -ne 2 ]; then
  echo 'Usage: backup-metadata.sh SERVICE OUTPUT.dump' >&2
  exit 2
fi
dbhub_backup_service=$1
dbhub_backup_output=$2
case "$dbhub_backup_service" in ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid libpq service name' >&2; exit 2;; esac
case "$dbhub_backup_output" in /*) ;; *) echo 'Backup output must be an absolute path' >&2; exit 2;; esac
if [ -e "$dbhub_backup_output" ] || [ -L "$dbhub_backup_output" ]; then
  echo 'Refusing to overwrite an existing backup' >&2
  exit 1
fi
umask 077
dbhub_backup_temp=$(mktemp "${dbhub_backup_output}.tmp.XXXXXX")
trap 'rm -f -- "$dbhub_backup_temp"' EXIT HUP INT TERM
pg_dump --dbname="service=$dbhub_backup_service" --format=custom --no-owner --no-acl --file="$dbhub_backup_temp"
pg_restore --list "$dbhub_backup_temp" >/dev/null
# A hard link atomically refuses an existing destination, including a symlink.
ln -- "$dbhub_backup_temp" "$dbhub_backup_output"
echo 'Metadata backup completed'
