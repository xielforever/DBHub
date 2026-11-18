#!/usr/bin/env bash
set -euo pipefail
# Full offline regression with real temporary PG fixtures. Source collection
# enumerates schemas, so packages must not create/drop them concurrently.
# Keep each original assertion and timeout. The large server package runs in
# two disjoint processes; record its complete discovery and exact selection so
# a partitioned result cannot be confused with a monolithic package pass.
run_id=${IAM_REGRESSION_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}
[[ "$run_id" =~ ^[A-Za-z0-9_-]+$ ]]
prefix="/evidence/regression-${run_id}"
test ! -e "${prefix}-packages.txt"
go list ./... > "${prefix}-packages.txt"
server_package=$(go list ./internal/server)
go test -p=1 -list '^(Test|Fuzz|Example)' ./internal/server > "${prefix}-server-discovery.txt"
roots=()
while IFS= read -r line;do
 if [[ "$line" =~ ^(Test|Fuzz|Example)[A-Za-z0-9_]*$ ]];then roots+=("$line");fi
done < "${prefix}-server-discovery.txt"
test "${#roots[@]}" -gt 1
printf '%s\n' "${roots[@]}" > "${prefix}-server-roots.txt"
packages=()
while IFS= read -r package;do
 if test "$package" != "$server_package";then packages+=("$package");fi
done < "${prefix}-packages.txt"
go test -p=1 -count=1 -timeout=720s -json "${packages[@]}"
middle=$(((${#roots[@]}+1)/2))
for part in 1 2;do
 if test "$part" = 1;then selection=("${roots[@]:0:middle}");else selection=("${roots[@]:middle}");fi
 printf '%s\n' "${selection[@]}" > "${prefix}-server-part${part}.txt"
 expression=$(IFS='|';echo "${selection[*]}")
 go test -p=1 -count=1 -timeout=720s -json ./internal/server -run "^(${expression})$"
done
