#!/bin/sh
set -eu
if [ "$#" -ne 2 ]; then
  echo 'Usage: restore-metadata.sh SERVICE BACKUP.dump' >&2
  exit 2
fi
dbhub_restore_service=$1
dbhub_restore_archive=$2
case "$dbhub_restore_service" in ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid libpq service name' >&2; exit 2;; esac
if [ ! -r "$dbhub_restore_archive" ]; then echo 'Backup is not readable' >&2; exit 2; fi
dbhub_restore_occupied=$(psql --dbname="service=$dbhub_restore_service" -X -At -v ON_ERROR_STOP=1 -c "SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%' AND c.relkind IN('r','p','v','m','S','f'))")
if [ "$dbhub_restore_occupied" != f ]; then
  echo 'Refusing to restore into a database containing user tables, views or sequences' >&2
  exit 1
fi
# Restore to a separate empty database. No --clean, no reverse migrations and no
# dependency on another process's short-lived dbhub_runtime role or privileges.
pg_restore --dbname="service=$dbhub_restore_service" --no-owner --no-acl --single-transaction --exit-on-error "$dbhub_restore_archive"
echo 'Metadata restore completed; start the matching application to provision runtime privileges'
