#!/usr/bin/env bash
# Arena 专属一键启动脚本 - 沙箱重启后可重复执行
# 功能：启动 Mock 后端（8080）+ 前端 Vite（5173），支持真实 Go 后端自动切换
# 使用：bash scripts/arena-start.sh  或  ./scripts/arena-start.sh
set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mkdir -p logs

# 环境变量（Arena 默认）
export PORT="${PORT:-8080}"
export MOCK_PORT="${MOCK_PORT:-8080}"
export VITE_PORT="${VITE_PORT:-5173}"
export VITE_API_PROXY_TARGET="${VITE_API_PROXY_TARGET:-http://localhost:8080}"

echo "=== DBHub Arena 启动脚本 ==="
echo "ROOT: $ROOT"
echo "MOCK_PORT: $MOCK_PORT, VITE_PORT: $VITE_PORT"
echo "VITE_API_PROXY_TARGET: $VITE_API_PROXY_TARGET"
echo ""

# 1. 检查 Go 是否可用，尝试启动真实后端
GO_BIN=""
for p in /usr/local/go/bin/go /opt/go/bin/go /home/user/sdk/go/bin/go /home/user/go/bin/go $(which go 2>/dev/null); do
  if [ -x "$p" ]; then GO_BIN="$p"; break; fi
done

REAL_BACKEND_PID=""
if [ -n "$GO_BIN" ] && [ -f "$ROOT/backend/cmd/server/main.go" ]; then
  echo "[1/3] 检测到 Go: $GO_BIN，尝试启动真实后端..."
  # 检查是否已有 8080 占用
  if lsof -i :$PORT >/dev/null 2>&1 || ss -ltn | grep -q ":$PORT "; then
    echo "  端口 $PORT 已占用，跳过真实后端启动，改用 Mock"
  else
    # 尝试 go run（需要 DATABASE_URL 可空，内存模式）
    # 后台启动，日志到 logs/backend.log
    set +e
    (cd "$ROOT/backend" && $GO_BIN run ./cmd/server 2>&1 | tee "$ROOT/logs/backend.log") &
    REAL_BACKEND_PID=$!
    set -e
    echo "  真实后端 PID $REAL_BACKEND_PID，等待 3s..."
    sleep 3
    if kill -0 $REAL_BACKEND_PID 2>/dev/null && (ss -ltn | grep -q ":$PORT " || lsof -i :$PORT >/dev/null 2>&1); then
      echo "  ✅ 真实后端已启动（: $PORT）"
    else
      echo "  ⚠️ 真实后端未就绪，回退到 Mock 后端"
      kill $REAL_BACKEND_PID 2>/dev/null || true
      REAL_BACKEND_PID=""
    fi
  fi
else
  echo "[1/3] 未检测到 Go 或后端源码，跳过真实后端"
fi

# 2. Mock 后端（Arena 必备）
if [ -z "$REAL_BACKEND_PID" ]; then
  echo "[2/3] 启动 Mock 后端 :$MOCK_PORT ..."
  # 杀掉旧的 mock
  pkill -f "mock-backend.mjs" 2>/dev/null || true
  # 检查端口占用
  if lsof -i :$MOCK_PORT >/dev/null 2>&1 || ss -ltn 2>/dev/null | grep -q ":$MOCK_PORT "; then
    echo "  端口 $MOCK_PORT 已占用，尝试释放..."
    fuser -k ${MOCK_PORT}/tcp 2>/dev/null || true
    sleep 1
  fi
  nohup node "$ROOT/scripts/mock-backend.mjs" > "$ROOT/logs/mock-backend.log" 2>&1 &
  MOCK_PID=$!
  echo "  Mock PID $MOCK_PID，日志 logs/mock-backend.log"
  # 等待端口
  for i in {1..10}; do
    if ss -ltn 2>/dev/null | grep -q ":$MOCK_PORT " || lsof -i :$MOCK_PORT >/dev/null 2>&1; then
      echo "  ✅ Mock 后端就绪"
      break
    fi
    sleep 0.5
  done
  # 健康检查
  curl -s http://localhost:$MOCK_PORT/api/health | head -c 200 || echo "  (health check pending)"
  echo ""
else
  echo "[2/3] 真实后端已运行，跳过 Mock"
fi

# 3. 前端
echo "[3/3] 启动前端 Vite :$VITE_PORT ..."
if [ ! -d "$ROOT/frontend/node_modules" ]; then
  echo "  未检测到 node_modules，执行 npm install..."
  (cd "$ROOT/frontend" && npm install)
fi

# 杀掉旧的前端
pkill -f "vite.*$VITE_PORT" 2>/dev/null || true
fuser -k ${VITE_PORT}/tcp 2>/dev/null || true
sleep 0.5

# 启动前端（host 0.0.0.0，allowedHosts true 已在 vite.config.ts 配置）
nohup bash -c "cd $ROOT/frontend && VITE_API_PROXY_TARGET=$VITE_API_PROXY_TARGET VITE_PORT=$VITE_PORT npm run dev -- --port $VITE_PORT --host 0.0.0.0" > "$ROOT/logs/frontend.log" 2>&1 &
FRONT_PID=$!
echo "  前端 PID $FRONT_PID，日志 logs/frontend.log"

for i in {1..15}; do
  if ss -ltn 2>/dev/null | grep -q ":$VITE_PORT " || lsof -i :$VITE_PORT >/dev/null 2>&1; then
    echo "  ✅ 前端就绪"
    break
  fi
  sleep 0.5
done

echo ""
echo "=== 启动完成 ==="
echo "Mock/Real 后端: http://localhost:${MOCK_PORT}  (health: /api/health)"
echo "前端 Vite:     http://localhost:${VITE_PORT}"
echo "Arena 预览:"
echo "  前端: https://\${PORT:-5173}-\${SANDBOX_ID}.e2b.app  (实际端口由平台注入)"
echo "  本地: http://localhost:${VITE_PORT}"
echo ""
echo "日志:"
echo "  tail -f logs/mock-backend.log logs/frontend.log logs/backend.log"
echo ""
echo "停止: bash scripts/arena-stop.sh"
