#!/usr/bin/env bash
set -euo pipefail
[[ $# == 2 ]] || exit 2
base=$1; days=$2
[[ $base == /* && ! -L $base && $days =~ ^[1-9][0-9]{0,3}$ && $days -le 3650 ]] || exit 2
base=$(realpath -e -- "$base")
[[ -f "$base/.dbhub-backup-root" && ! -L "$base/.dbhub-backup-root" && $(cat "$base/.dbhub-backup-root") == dbhub-local-recovery-v1 ]] || exit 2
seen=0
while IFS= read -r old; do
 [[ -f "$old/manifest.json" && -f "$old/SHA256SUMS" && ! -L $old ]] || continue
 [[ $(basename -- "$old") =~ ^bundle-[0-9]{8}T[0-9]{6}Z-[0-9a-f-]{36}$ ]] || continue
 grep -Fq '"format":"dbhub-local-recovery-v1"' "$old/manifest.json" || continue
 # Corrupt archives are retained for diagnosis and do not count toward the two
 # recoverable archives that must remain after pruning.
 (cd -- "$old"; sha256sum -c SHA256SUMS >/dev/null 2>&1) || continue
 seen=$((seen+1)); [[ $seen -gt 2 ]] || continue
 [[ -n $(find "$old" -maxdepth 0 -type d -mtime +"$days" -print) ]] || continue
 resolved=$(realpath -e -- "$old")
 [[ $resolved == "$base"/bundle-* && ! -L $old ]] || exit 1
 rm -rf -- "$resolved"
done < <(find "$base" -mindepth 1 -maxdepth 1 -type d -name 'bundle-*' -print | LC_ALL=C sort -r)
