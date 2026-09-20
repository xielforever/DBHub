#!/usr/bin/env bash
# Arena 快捷入口：bash arena.sh
set -e
ROOT="$(cd "$(dirname "$0")" && pwd)"
bash "$ROOT/scripts/arena-start.sh" "$@"
