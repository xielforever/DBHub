#!/usr/bin/env bash
# Arena 停止脚本
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
echo "停止 DBHub Arena 服务..."
pkill -f "mock-backend.mjs" 2>/dev/null || true
pkill -f "vite.*5173" 2>/dev/null || true
pkill -f "backend/cmd/server" 2>/dev/null || true
pkill -f "go run" 2>/dev/null || true
fuser -k 8080/tcp 2>/dev/null || true
fuser -k 5173/tcp 2>/dev/null || true
echo "已尝试释放 8080/5173"
ps aux | grep -E "mock-backend|vite|go run" | grep -v grep || true
echo "完成"
