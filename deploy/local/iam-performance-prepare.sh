#!/bin/bash
set -euo pipefail
case "${1:-}" in
 init)
  psql -v ON_ERROR_STOP=1 -c 'CREATE SCHEMA IF NOT EXISTS iam_perf_baseline;'
  ;;
 seed)
  psql -v ON_ERROR_STOP=1 -f /fixture/seed.sql
  ;;
 clone)
  test "$(psql -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='iam_perf_candidate_v2'")" = 0
  pg_dump --schema=iam_perf_baseline --no-owner --no-privileges | sed 's/iam_perf_baseline/iam_perf_candidate_v2/g' | psql -q -v ON_ERROR_STOP=1
  ;;
 target)
  psql -v ON_ERROR_STOP=1 -f /fixture/target.sql
  ;;
 verify)
  psql -At -v ON_ERROR_STOP=1 -f /fixture/verify.sql
  ;;
 fingerprint)
  psql -At -v ON_ERROR_STOP=1 -f /fixture/schema.sql
  ;;
 *) exit 2;;
esac
