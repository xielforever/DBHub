#!/bin/sh
# Text mode preserves govulncheck's nonzero exit for affected imported packages.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

# A replacement changes the module path queried in the advisory database.
# Require canonical modules rather than silently reporting an incomplete scan.
dbhub_replacements=$(GOFLAGS=-mod=readonly go list -m -f '{{if .Replace}}{{.Path}}{{end}}' all)
if [ -n "$(printf '%s' "$dbhub_replacements" | tr -d '[:space:]')" ]; then
    echo 'Vulnerability checks require canonical dependencies without module replacements.' >&2
    exit 1
fi

dbhub_tools=$(mktemp -d "${TMPDIR:-/tmp}/dbhub-govulncheck.XXXXXX")
trap 'rm -rf -- "$dbhub_tools"' EXIT
GOFLAGS=-mod=mod GOBIN="$dbhub_tools" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOFLAGS=-mod=vendor "$dbhub_tools/govulncheck" -scan=package ./...
