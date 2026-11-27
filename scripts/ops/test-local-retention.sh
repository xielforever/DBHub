#!/usr/bin/env bash
set -euo pipefail
umask 077
folder=$(mktemp -d /home/dzz/.local/share/dbhub/drills/retention-check-XXXXXX)
printf '%s\n' dbhub-local-recovery-v1 >"$folder/.dbhub-backup-root"
for day in 01 02 03 04; do
 name="bundle-202609${day}T000000Z-00000000-0000-0000-0000-0000000000${day}"
 mkdir -m 700 "$folder/$name"
 printf '{"format":"dbhub-local-recovery-v1"}\n' >"$folder/$name/manifest.json"
 (cd "$folder/$name"; sha256sum manifest.json >SHA256SUMS)
 touch -d '30 days ago' "$folder/$name"
done
pruner=$(dirname -- "$(realpath -e -- "$0")")/prune-local-backups.sh
bash "$pruner" "$folder" 14
[[ $(find "$folder" -mindepth 1 -maxdepth 1 -type d | wc -l) == 2 ]]
[[ -d "$folder/bundle-20260903T000000Z-00000000-0000-0000-0000-000000000003" && -d "$folder/bundle-20260904T000000Z-00000000-0000-0000-0000-000000000004" ]]
bash "$pruner" "$folder" 14
[[ $(find "$folder" -mindepth 1 -maxdepth 1 -type d | wc -l) == 2 ]]
resolved=$(realpath -e -- "$folder")
[[ $resolved == /home/dzz/.local/share/dbhub/drills/retention-check-* ]] || exit 1
rm -rf -- "$resolved"
printf '{"status":"passed","retention_days":14,"expired_archives_removed":2,"minimum_valid_archives_preserved":2,"repeated_prune_preserves_minimum":true}\n'
