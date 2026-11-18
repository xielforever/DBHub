#!/usr/bin/env bash
# Local WSL recovery bundle. All application/database work remains in Docker.
# A short application pause keeps metadata and its pending audit spool together.
set -Eeuo pipefail
umask 077
[[ ${1:-} == --local && $# == 1 ]] || { echo 'Usage: backup-local-bundle.sh --local' >&2; exit 2; }
repo=$(realpath -e -- "${DBHUB_LOCAL_REPO:-/mnt/c/dev/projects/DBHub}")
base=${DBHUB_LOCAL_BACKUP_DIR:-$HOME/.local/share/dbhub/backups}
keep_days=${DBHUB_LOCAL_BACKUP_RETENTION_DAYS:-14}
backend=${DBHUB_LOCAL_BACKEND_CONTAINER:-dbhub-local-backend-1}
metadata=${DBHUB_LOCAL_METADATA_CONTAINER:-dbhub-local-metadata-1}
business=${DBHUB_LOCAL_BUSINESS_CONTAINER:-dbhub-wsl-demo-postgres-1}
for name in "$backend" "$metadata" "$business"; do
 [[ $name =~ ^dbhub-[a-zA-Z0-9_.-]+$ ]] || exit 2
done
[[ $keep_days =~ ^[0-9]+$ && $keep_days -ge 1 && $keep_days -le 3650 ]] || exit 2
[[ -f "$repo/.dbhub/local/compose.yaml" && -s "$repo/.env.local" ]] || { echo 'Local configuration missing' >&2; exit 2; }
[[ $base == /* && ! -L $base ]] || exit 2
[[ $base != / && $base != /home/dzz && $base != "$repo" && ! -L "$base/.dbhub-backup-root" ]] || exit 2
mkdir -p -- "$base"; chmod 700 -- "$base"; base=$(realpath -e -- "$base")
printf '%s\n' dbhub-local-recovery-v1 >"$base/.dbhub-backup-root"
exec 9>"$base/.worker.lock"
flock -n 9 || { echo 'A local backup is already running' >&2; exit 1; }
id=$(cat /proc/sys/kernel/random/uuid)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
name="bundle-$stamp-$id"
stage=$(mktemp -d "$base/.incomplete-$id.XXXXXX")
paused=0
resume_marker="$base/.paused-${INVOCATION_ID:-$$}"
report() {
 docker exec -i "$metadata" psql -X -q -U dbhub -d dbhub_meta -v ON_ERROR_STOP=1 -v outcome="$1" -v bundle="$name" -v run_id="$id" -v keep_days="$keep_days" >/dev/null <<'SQL'
BEGIN;
INSERT INTO sys_operations(name,status,started_at,finished_at,last_success_at,details)
VALUES('backup',:'outcome',now(),CASE WHEN :'outcome'='running' THEN NULL ELSE now() END,CASE WHEN :'outcome'='succeeded' THEN now() END,jsonb_build_object('bundle',:'bundle','scope','metadata,business,analytics,audit,application-key','retention_days',:'keep_days'::integer,'minimum_bundles',2))
ON CONFLICT(name) DO UPDATE SET status=EXCLUDED.status,started_at=CASE WHEN EXCLUDED.status='running' THEN now() ELSE sys_operations.started_at END,finished_at=EXCLUDED.finished_at,last_success_at=COALESCE(EXCLUDED.last_success_at,sys_operations.last_success_at),details=EXCLUDED.details,updated_at=now();
INSERT INTO sys_audit_logs(event_id,created_at,workspace_id,action,resource_type,resource_name,status,context)
SELECT :'run_id'::uuid,s.started_at,0,'LOCAL_RECOVERY_BUNDLE','OPERATIONS','本机完整恢复备份',CASE WHEN :'outcome'='succeeded' THEN 1 ELSE 0 END,jsonb_build_object('actor_username','本机备份服务','actor_role','system','outcome',:'outcome')
FROM sys_operations s WHERE s.name='backup' AND :'outcome'<>'running' ON CONFLICT(event_id,created_at) DO NOTHING;
COMMIT;
SQL
}
finish() {
 status=$?
 trap - EXIT
 if [[ $paused == 1 ]]; then if docker unpause "$backend" >/dev/null; then rm -f -- "$resume_marker"; else status=1; fi; else rm -f -- "$resume_marker"; fi
 if [[ $status != 0 ]]; then report failed 2>/dev/null || true; echo 'Local backup failed; previous bundles preserved' >&2; fi
 exit "$status"
}
trap finish EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
report running
state=$(docker inspect -f '{{.State.Status}}' "$backend")
[[ $state == running || $state == exited ]] || { echo 'Backend must be running or stopped' >&2; exit 1; }
if [[ $state == running ]]; then printf '%s\n' "$backend" >"$resume_marker"; docker pause "$backend" >/dev/null; paused=1; fi
audit_volume=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/dbhub/audit"}}{{.Name}}{{end}}{{end}}' "$backend")
[[ $audit_volume =~ ^[a-zA-Z0-9_.-]+$ ]] || { echo 'Dedicated audit volume missing' >&2; exit 1; }
docker run --rm --entrypoint tar -v "$audit_volume:/audit:ro" dbhub-local/postgres:16-existing -C /audit -czf - . >"$stage/audit.tar.gz"
docker exec "$metadata" pg_dumpall -U dbhub --roles-only >"$stage/metadata-roles.sql"
docker exec "$business" pg_dumpall -U demo_admin --roles-only >"$stage/business-roles.sql"
docker exec "$metadata" pg_dump -U dbhub -d dbhub_meta --format=custom >"$stage/dbhub_meta.dump"
for database in dbhub_demo tms wms dbhub_analytics; do
 docker exec "$business" pg_dump -U demo_admin -d "$database" --format=custom >"$stage/$database.dump"
done
docker cp "$backend:/out/server" - | gzip -n >"$stage/server.tar.gz"
docker cp "$backend:/out/healthcheck" - | gzip -n >"$stage/healthcheck.tar.gz"
cp -- "$repo/.env.local" "$stage/application.env"
chmod 600 -- "$stage/application.env"
cp -- "$repo/.dbhub/local/compose.yaml" "$stage/local-compose.yaml"
if [[ -f "$repo/.dbhub/simulation/department-credentials.json" ]]; then cp -- "$repo/.dbhub/simulation/department-credentials.json" "$stage/demo-identities.json"; chmod 600 -- "$stage/demo-identities.json"; fi
if [[ $paused == 1 ]]; then docker unpause "$backend" >/dev/null; paused=0; rm -f -- "$resume_marker"; fi
for database in dbhub_meta dbhub_demo tms wms dbhub_analytics; do
 docker run --rm --entrypoint pg_restore -v "$stage:/bundle:ro" dbhub-local/postgres:16-existing --list "/bundle/$database.dump" >"$stage/$database.toc"
done
commit=$(git -C "$repo" rev-parse HEAD)
[[ $commit =~ ^[0-9a-f]{40}$ ]] || exit 1
printf '{"format":"dbhub-local-recovery-v1","created_at":"%s","baseline_commit":"%s","matching_server_included":true,"databases":["dbhub_meta","dbhub_demo","tms","wms","dbhub_analytics"],"audit_included":true,"application_key_included":true,"retention_days":%s,"minimum_bundles":2}\n' "$stamp" "$commit" "$keep_days" >"$stage/manifest.json"
(cd -- "$stage"; find . -maxdepth 1 -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum >SHA256SUMS; sha256sum -c SHA256SUMS >/dev/null)
destination="$base/$name"
[[ ! -e $destination && ! -L $destination ]] || exit 1
mv -T -- "$stage" "$destination"
report succeeded
bash "$(dirname -- "$(realpath -e -- "$0")")/prune-local-backups.sh" "$base" "$keep_days"
printf '{"backup":"succeeded","bundle":"%s","scope":"5 databases + roles + audit + app key + matching binary"}\n' "$name"
