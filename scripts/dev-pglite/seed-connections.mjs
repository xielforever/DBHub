/**
 * 幂等预置演示数据源（通过后端 API，口令走后端 AES-GCM 加密落库）：
 *  - admin 建 5 条团队共享连接：1 条本机 PGlite 可连示例 + 4 条内网不可达示例
 *    （不可达示例用于演示连接失败时的中文友好报错，勿改成可达地址）
 *  - dev1 建 1 条本机 PGlite 可连示例（共享后全员可见）
 * 已存在同名连接则更新，可反复执行。
 */
const BASE = process.env.API_BASE || 'http://127.0.0.1:8080/api/v1';

async function login(username, password) {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  const body = await res.json();
  if (body.code !== 0) throw new Error(`登录失败 ${username}: ${body.message}`);
  return body.data.access_token;
}

async function api(token, path, method = 'GET', payload) {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: payload ? JSON.stringify(payload) : undefined,
  });
  const body = await res.json();
  if (body.code !== 0) throw new Error(`${method} ${path} -> ${body.code} ${body.message}`);
  return body.data;
}

async function upsert(token, conn) {
  const list = (await api(token, '/connections?page=1&page_size=100')).items || [];
  const hit = list.find((c) => c.name === conn.name);
  if (hit) {
    await api(token, `/connections/${hit.id}`, 'PUT', conn);
    console.log(`[seed] 更新连接 #${hit.id} ${conn.name}`);
  } else {
    const data = await api(token, '/connections', 'POST', conn);
    console.log(`[seed] 新建连接 #${data.id ?? '?'} ${conn.name}`);
  }
}

const adminConns = [
  {
    name: '订单核心库（示例·可连接）',
    type: 'postgres', host: '127.0.0.1', port: 5432, database: 'postgres',
    username: 'postgres', password: 'postgres',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'violet',
  },
  {
    name: '会员分析库（MySQL 内网示例）',
    type: 'mysql', host: '10.12.3.21', port: 3306, database: 'member_analytics',
    username: 'analytics_ro', password: 'Demo#2026',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'blue',
  },
  {
    name: '缓存集群（Redis 内网示例）',
    type: 'redis', host: '10.12.3.22', port: 6379, database: '',
    username: '', password: 'Demo#2026',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'rose',
  },
  {
    name: '订单只读从库（PostgreSQL 内网示例）',
    type: 'postgres', host: '10.12.3.23', port: 5432, database: 'orders',
    username: 'readonly', password: 'Demo#2026',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'amber',
  },
  {
    name: '应用日志库（MySQL 内网示例）',
    type: 'mysql', host: '10.12.3.24', port: 3306, database: 'app_logs',
    username: 'log_reader', password: 'Demo#2026',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'slate',
  },
];

const devConns = [
  {
    name: '开发示例库（可连接）',
    type: 'postgres', host: '127.0.0.1', port: 5432, database: 'postgres',
    username: 'postgres', password: 'postgres',
    ssl_mode: 'disable', connection_timeout: 5, color_label: 'emerald',
  },
];

const admin = await login('admin', 'admin123');
for (const c of adminConns) await upsert(admin, c);

const dev1 = await login('dev1', 'dev12345!');
for (const c of devConns) await upsert(dev1, c);

console.log('[seed] 演示数据源就绪');
