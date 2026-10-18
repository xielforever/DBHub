#!/bin/sh
# A single private backup volume is owned by one worker. libpq credentials are
# supplied through PGSERVICEFILE/PGPASSFILE; neither secrets nor raw errors log.
set -eu
umask 077
dbhub_backup_dir=${DBHUB_BACKUP_DIR:-/var/lib/dbhub/backups}
dbhub_backup_service=${PGSERVICE:-dbhub}
dbhub_backup_interval=${DBHUB_BACKUP_INTERVAL_SECONDS:-86400}
dbhub_backup_retry=${DBHUB_BACKUP_RETRY_SECONDS:-60}
dbhub_backup_timeout=${DBHUB_BACKUP_TIMEOUT_SECONDS:-900}
dbhub_backup_keep_days=${DBHUB_BACKUP_RETENTION_DAYS:-14}
dbhub_backup_once=${DBHUB_BACKUP_ONCE:-0}
for dbhub_backup_number in "$dbhub_backup_interval" "$dbhub_backup_retry" "$dbhub_backup_timeout" "$dbhub_backup_keep_days"; do
 case "$dbhub_backup_number" in ''|*[!0-9]*) echo 'Backup timing values must be integers' >&2; exit 2;; esac
done
[ "$dbhub_backup_interval" -ge 60 ] && [ "$dbhub_backup_interval" -le 604800 ] || exit 2
[ "$dbhub_backup_retry" -ge 10 ] && [ "$dbhub_backup_retry" -le 3600 ] || exit 2
[ "$dbhub_backup_timeout" -ge 30 ] && [ "$dbhub_backup_timeout" -le 7200 ] || exit 2
[ "$dbhub_backup_keep_days" -ge 1 ] && [ "$dbhub_backup_keep_days" -le 3650 ] || exit 2
case "$dbhub_backup_once" in 0|1) ;; *) exit 2;; esac
case "$dbhub_backup_service" in ''|*[!a-zA-Z0-9_.-]*) exit 2;; esac
case "$dbhub_backup_dir" in /*) ;; *) echo 'Backup directory must be absolute' >&2; exit 2;; esac
if [ -L "$dbhub_backup_dir" ]; then echo 'Backup directory must not be a symlink' >&2; exit 2; fi
mkdir -p "$dbhub_backup_dir"
chmod 700 "$dbhub_backup_dir"
exec 9>"$dbhub_backup_dir/.worker.lock"
if ! flock -n 9; then echo 'Another backup worker owns this volume' >&2; exit 1; fi
export PGCONNECT_TIMEOUT=5
dbhub_backup_script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
trap 'exit 0' HUP INT TERM

dbhub_backup_report() {
 dbhub_backup_status=$1
 dbhub_backup_file=$2
 dbhub_backup_bytes=$3
 dbhub_backup_hash=$4
 # psql variable quoting is applied by stdin processing, never string assembly.
 psql --dbname="service=$dbhub_backup_service" -X -q --set=ON_ERROR_STOP=1 \
  --set=backup_id="$dbhub_backup_id" --set=run_status="$dbhub_backup_status" --set=backup_file="$dbhub_backup_file" \
  --set=backup_bytes="$dbhub_backup_bytes" --set=backup_sha="$dbhub_backup_hash" \
  >"$dbhub_backup_dir/.status-log" 2>&1 <<'SQL'
BEGIN;
INSERT INTO sys_operations(name,status,finished_at,last_success_at,details)
VALUES('backup',:'run_status',CASE WHEN :'run_status'='running' THEN NULL ELSE now() END,CASE WHEN :'run_status'='succeeded' THEN now() ELSE NULL END,
 jsonb_build_object('file',:'backup_file','bytes',:'backup_bytes'::bigint,'sha256',:'backup_sha'))
ON CONFLICT(name) DO UPDATE SET status=EXCLUDED.status,
 started_at=CASE WHEN EXCLUDED.status='running' THEN now() ELSE sys_operations.started_at END,
 finished_at=EXCLUDED.finished_at,
 last_success_at=COALESCE(EXCLUDED.last_success_at,sys_operations.last_success_at),details=EXCLUDED.details,updated_at=now();
INSERT INTO sys_audit_logs(event_id,created_at,workspace_id,action,resource_type,resource_name,status,context)
SELECT :'backup_id'::uuid,s.started_at,0,'METADATA_BACKUP','OPERATIONS','元数据库自动备份',CASE WHEN :'run_status'='succeeded' THEN 1 ELSE 0 END,
 jsonb_build_object('actor_username','备份服务','actor_role','system','outcome',:'run_status')
FROM sys_operations s WHERE s.name='backup' AND :'run_status'<>'running'
ON CONFLICT(event_id,created_at) DO NOTHING;
COMMIT;
SQL
}

dbhub_backup_prune() {
 # Retain at least two completed archives even when both are beyond retention.
 dbhub_backup_seen=0
 find "$dbhub_backup_dir" -maxdepth 1 -type f -name 'metadata-*.dump' >"$dbhub_backup_dir/.archives-unsorted" || return 1
 LC_ALL=C sort -r "$dbhub_backup_dir/.archives-unsorted" >"$dbhub_backup_dir/.archives" || return 1
 rm -f -- "$dbhub_backup_dir/.archives-unsorted" || return 1
 while IFS= read -r dbhub_backup_old; do
  dbhub_backup_basename=${dbhub_backup_old##*/}
  if ! printf '%s\n' "$dbhub_backup_basename" | grep -Eq '^metadata-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.dump$'; then continue; fi
  [ -f "$dbhub_backup_old.sha256" ] && [ ! -L "$dbhub_backup_old.sha256" ] || continue
  dbhub_backup_seen=$((dbhub_backup_seen+1))
  if [ "$dbhub_backup_seen" -gt 2 ] && [ -n "$(find "$dbhub_backup_old" -maxdepth 0 -type f -mtime +"$dbhub_backup_keep_days" -print)" ]; then
   rm -f -- "$dbhub_backup_old" "$dbhub_backup_old.sha256" || return 1
  fi
 done <"$dbhub_backup_dir/.archives"
 rm -f -- "$dbhub_backup_dir/.archives"
}

while :; do
 dbhub_backup_id=$(cat /proc/sys/kernel/random/uuid)
 dbhub_backup_name="metadata-$(date -u +%Y%m%dT%H%M%SZ)-$dbhub_backup_id.dump"
 dbhub_backup_output="$dbhub_backup_dir/$dbhub_backup_name"
 dbhub_backup_ok=0
 if dbhub_backup_report running '' 0 ''; then
  if timeout -s TERM "$dbhub_backup_timeout" sh "$dbhub_backup_script_dir/backup-metadata.sh" "$dbhub_backup_service" "$dbhub_backup_output" >"$dbhub_backup_dir/.backup-log" 2>&1; then
   if sha256sum "$dbhub_backup_output" >"$dbhub_backup_output.sha256.tmp" && mv -- "$dbhub_backup_output.sha256.tmp" "$dbhub_backup_output.sha256"; then
    dbhub_backup_sum=$(cut -d ' ' -f 1 "$dbhub_backup_output.sha256")
    dbhub_backup_size=$(wc -c <"$dbhub_backup_output" | tr -d ' ')
    if dbhub_backup_report succeeded "$dbhub_backup_name" "$dbhub_backup_size" "$dbhub_backup_sum"; then
     date +%s >"$dbhub_backup_dir/.last-success"
     if dbhub_backup_prune; then dbhub_backup_ok=1; printf succeeded >"$dbhub_backup_dir/.last-result"; fi
     printf '{"backup":"succeeded","bytes":%s}\n' "$dbhub_backup_size"
    fi
   fi
  fi
 fi
 if [ "$dbhub_backup_ok" -eq 0 ]; then
  printf failed >"$dbhub_backup_dir/.last-result"
  dbhub_backup_report failed '' 0 '' || true
  echo '{"backup":"failed","previous_archives_preserved":true}' >&2
 fi
 if [ "$dbhub_backup_once" -eq 1 ]; then [ "$dbhub_backup_ok" -eq 1 ]; exit; fi
 if [ "$dbhub_backup_ok" -eq 1 ]; then sleep "$dbhub_backup_interval" & else sleep "$dbhub_backup_retry" & fi
 wait "$!" || true
done
