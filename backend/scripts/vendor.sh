#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
GOFLAGS=-mod=mod go mod vendor
# Go's vendor command omits C-only include directories. Copy the original
# headers from the checksum-verified module cache; never rewrite package code.
dbhub_parser_module="$(GOFLAGS=-mod=mod go list -m -f '{{.Dir}}' github.com/pganalyze/pg_query_go/v5)"
cp -R "$dbhub_parser_module/parser/include" vendor/github.com/pganalyze/pg_query_go/v5/parser/include
