#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
scripts/cloud/dev.sh up -d backend frontend
python3 - <<'PY'
import time,urllib.request
for _ in range(60):
 try:
  with urllib.request.urlopen('http://127.0.0.1:8080/api/health',timeout=2) as response: assert response.status==200
  with urllib.request.urlopen('http://127.0.0.1:5173/',timeout=2) as response: assert response.status==200
  print('DBHub services ready')
  break
 except Exception: time.sleep(1)
else: raise SystemExit('DBHub startup timed out; inspect scripts/cloud/dev.sh logs backend frontend')
PY
